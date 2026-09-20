package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type arrConfig struct {
	URL *url.URL
	Key string
}

type arrItem struct {
	SeriesTitle string `json:"series_title,omitempty"`
	ID          int64  `json:"id"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Year        int    `json:"year,omitempty"`
	SeriesID    int64  `json:"series_id,omitempty"`
	Season      int    `json:"season,omitempty"`
	Episode     int    `json:"episode,omitempty"`
	HasFile     bool   `json:"has_file"`
}

type arrResource struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Year          int    `json:"year"`
	SeriesID      int64  `json:"seriesId"`
	Season        int    `json:"seasonNumber"`
	Episode       int    `json:"episodeNumber"`
	HasFile       bool   `json:"hasFile"`
	MovieFileID   int64  `json:"movieFileId"`
	EpisodeFileID int64  `json:"episodeFileId"`
}

type arrClient struct {
	config    arrConfig
	http      *http.Client
	mu        sync.Mutex
	loading   chan struct{}
	catalogue []arrItem
	expires   time.Time
}

type arrService struct {
	clients   map[string]*arrClient
	mu        sync.Mutex
	previews  map[string]replacementPreview
	jobs      map[string]*replacementJob
	busy      map[string]bool
	directory string
	enabled   bool
}

func loadArrConfig() (map[string]arrConfig, error) {
	result := make(map[string]arrConfig)
	for _, source := range []string{"sonarr", "radarr"} {
		prefix := strings.ToUpper(source)
		address, key := strings.TrimSpace(os.Getenv(prefix+"_URL")), strings.TrimSpace(os.Getenv(prefix+"_API_KEY"))
		if address == "" && key == "" {
			continue
		}
		parsed, err := url.Parse(address)
		if err != nil || key == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%s_URL and %s_API_KEY must be set together; URL must be HTTP(S) without credentials, query or fragment", prefix, prefix)
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		result[source] = arrConfig{URL: parsed, Key: key}
	}
	return result, nil
}

func newArrService(configs map[string]arrConfig) *arrService {
	service := &arrService{clients: make(map[string]*arrClient), previews: make(map[string]replacementPreview), jobs: make(map[string]*replacementJob), busy: make(map[string]bool)}
	for source, config := range configs {
		service.clients[source] = &arrClient{config: config, loading: make(chan struct{}, 1), http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	}
	return service
}

func (client *arrClient) request(ctx context.Context, method, endpoint string, query url.Values, input, output any) (int, error) {
	upstream := *client.config.URL
	upstream.Path = strings.TrimRight(upstream.Path, "/") + "/api/v3/" + endpoint
	upstream.RawQuery = query.Encode()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return 0, errors.New("invalid Arr request")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, upstream.String(), bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("invalid Arr request")
	}
	request.Header.Set("X-Api-Key", client.config.Key)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return 0, errors.New("Arr request could not be confirmed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("Arr returned HTTP %d", response.StatusCode)
	}
	if resources, ok := output.(*[]arrResource); ok {
		return response.StatusCode, decodeArrCatalogue(response.Body, resources)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return response.StatusCode, errors.New("Arr response exceeds the limit or could not be read")
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return response.StatusCode, errors.New("invalid Arr response")
	}
	return response.StatusCode, nil
}

func decodeArrCatalogue(body io.Reader, resources *[]arrResource) error {
	limited := &io.LimitedReader{R: body, N: 64*1024*1024 + 1}
	decoder := json.NewDecoder(limited)
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('[') {
		return errors.New("invalid Arr catalogue")
	}
	items := make([]arrResource, 0)
	for decoder.More() {
		var item arrResource
		if len(items) >= 100000 || decoder.Decode(&item) != nil || limited.N <= 0 {
			return errors.New("Arr catalogue is invalid or exceeds the 64 MiB / 100000 item limit")
		}
		items = append(items, item)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(']') || decoder.Decode(&struct{}{}) != io.EOF || limited.N <= 0 {
		return errors.New("Arr catalogue is incomplete, invalid or exceeds 64 MiB")
	}
	*resources = items
	return nil
}

func (client *arrClient) items(ctx context.Context, source string, seriesID int64) ([]arrItem, error) {
	select {
	case client.loading <- struct{}{}:
		defer func() { <-client.loading }()
	case <-ctx.Done():
		return nil, errors.New("Arr catalogue request timed out")
	}
	client.mu.Lock()
	if seriesID == 0 && client.catalogue != nil && time.Now().Before(client.expires) {
		items := client.catalogue
		client.mu.Unlock()
		return items, nil
	}
	client.mu.Unlock()
	endpoint, kind := "movie", "movie"
	query := make(url.Values)
	if source == "sonarr" {
		endpoint, kind = "series", "series"
	}
	if seriesID > 0 {
		endpoint, kind = "episode", "episode"
		query.Set("seriesId", strconv.FormatInt(seriesID, 10))
	}
	var resources []arrResource
	if _, err := client.request(ctx, "GET", endpoint, query, nil, &resources); err != nil {
		return nil, err
	}
	if resources == nil {
		return nil, errors.New("invalid Arr catalogue")
	}
	items := make([]arrItem, 0, len(resources))
	for _, resource := range resources {
		if resource.ID <= 0 || (seriesID > 0 && resource.SeriesID != seriesID) {
			return nil, errors.New("invalid Arr item")
		}
		items = append(items, arrItem{ID: resource.ID, Source: source, Kind: kind, Title: resource.Title, Year: resource.Year, SeriesID: resource.SeriesID, Season: resource.Season, Episode: resource.Episode, HasFile: resource.HasFile})
	}
	if seriesID == 0 {
		client.mu.Lock()
		client.catalogue, client.expires = items, time.Now().Add(time.Minute)
		client.mu.Unlock()
	}
	return items, nil
}

func (app *application) libraryConfig(writer http.ResponseWriter, request *http.Request) {
	sources := make([]string, 0, 2)
	for _, source := range []string{"radarr", "sonarr"} {
		if app.arr.clients[source] != nil {
			sources = append(sources, source)
		}
	}
	app.arr.mu.Lock()
	enabled := app.arr.enabled
	app.arr.mu.Unlock()
	writeLibraryJSON(writer, http.StatusOK, map[string]any{"sources": sources, "replacements_enabled": enabled})
}

var arrEpisodeQuery = regexp.MustCompile(`(?i)^(.*?)\bs([0-9]{1,3})(?:\s*e([0-9]{1,4}))?$`)

func (app *application) libraryItems(writer http.ResponseWriter, request *http.Request) {
	_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(35 * time.Second))
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	source := request.URL.Query().Get("source")
	client := app.arr.clients[source]
	if client == nil {
		writeJSONError(writer, 400, "Integration is not configured")
		return
	}
	seriesID, offset := int64(0), 0
	if raw := request.URL.Query().Get("series_id"); raw != "" {
		var err error
		seriesID, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || seriesID <= 0 || source != "sonarr" {
			writeJSONError(writer, 400, "Invalid series")
			return
		}
	}
	if raw := request.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 100000 {
			writeJSONError(writer, 400, "Invalid offset")
			return
		}
	}
	search := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	if len(search) > 200 || (seriesID == 0 && len([]rune(search)) < 2) {
		writeJSONError(writer, 400, "Search must contain 2 to 200 characters")
		return
	}
	season, episode := -1, -1
	episodeQuery := ""
	if source == "sonarr" {
		if parts := arrEpisodeQuery.FindStringSubmatch(search); parts != nil {
			search = strings.Join(strings.Fields(parts[1]), " ")
			season, _ = strconv.Atoi(parts[2])
			episodeQuery = "s" + parts[2]
			if parts[3] != "" {
				episode, _ = strconv.Atoi(parts[3])
				episodeQuery += "e" + parts[3]
			}
			if seriesID == 0 && search == "" {
				writeJSONError(writer, 400, "Include a series title before the season or episode")
				return
			}
		}
	}
	items, err := client.items(ctx, source, seriesID)
	if err != nil {
		writeJSONError(writer, 502, err.Error())
		return
	}
	matches := make([]arrItem, 0)
	for _, item := range items {
		if search == "" || strings.Contains(strings.Join(strings.Fields(strings.ToLower(item.Title)), " "), search) {
			matches = append(matches, item)
		}
	}
	seriesTitle := ""
	if season >= 0 && seriesID == 0 {
		exact := make([]arrItem, 0)
		for _, item := range matches {
			if strings.EqualFold(strings.Join(strings.Fields(item.Title), " "), search) {
				exact = append(exact, item)
			}
		}
		if len(exact) > 0 {
			matches = exact
		}
		if len(matches) == 1 {
			seriesID, seriesTitle = matches[0].ID, matches[0].Title
			matches, err = client.items(ctx, source, seriesID)
			if err != nil {
				writeJSONError(writer, 502, err.Error())
				return
			}
		}
	}
	if season >= 0 && seriesID > 0 {
		filtered := make([]arrItem, 0)
		for _, item := range matches {
			if item.Season == season && (episode < 0 || item.Episode == episode) {
				item.SeriesTitle = seriesTitle
				filtered = append(filtered, item)
			}
		}
		matches = filtered
		sort.SliceStable(matches, func(left, right int) bool { return matches[left].Episode < matches[right].Episode })
	}
	start := min(offset, len(matches))
	end := min(start+50, len(matches))
	writeLibraryJSON(writer, 200, map[string]any{"items": matches[start:end], "has_more": end < len(matches), "next_offset": end, "episode_query": episodeQuery})
}

func writeLibraryJSON(writer http.ResponseWriter, code int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(code)
	_ = json.NewEncoder(writer).Encode(value)
}
