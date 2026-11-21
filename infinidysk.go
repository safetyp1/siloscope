package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ifdPollInterval = 2 * time.Second

type ifdRead struct {
	ID      string   `json:"id"`
	Name    string   `json:"fileName"`
	Fetched *float64 `json:"bytesFetched"`
	Rate    *float64 `json:"fetch_bps"`
}

type ifdSample struct {
	Timestamp int64    `json:"t"`
	Fetched   *float64 `json:"download"`
	Served    *float64 `json:"upload"`
}

type ifdSnapshot struct {
	Configured     bool        `json:"configured"`
	Available      bool        `json:"available"`
	FilesAvailable bool        `json:"files_available"`
	SampledAt      int64       `json:"sampled_at"`
	Connections    *float64    `json:"connections"`
	Files          []ifdRead   `json:"files"`
	History        []ifdSample `json:"history"`
}

type ifdLive struct {
	Timestamp int64   `json:"at"`
	Fetched   *uint64 `json:"fetch_bps"`
	Served    *uint64 `json:"read_bps"`
	Providers []struct {
		Active *uint64 `json:"active"`
	} `json:"providers"`
	Reads []struct {
		ID      *uint64 `json:"id"`
		Path    string  `json:"path"`
		Sources []struct {
			Bytes *uint64 `json:"bytes"`
			Rate  *uint64 `json:"bps"`
		} `json:"sources"`
	} `json:"reads"`
}

type ifdMonitor struct {
	mu     sync.RWMutex
	url    *url.URL
	key    string
	client *http.Client
	data   ifdSnapshot
}

func loadIFDConfig() (*url.URL, string, error) {
	raw, key := strings.TrimSpace(os.Getenv("IFD_URL")), strings.TrimSpace(os.Getenv("IFD_API_KEY"))
	if raw == "" && key == "" {
		return nil, "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || key == "" {
		return nil, "", errors.New("IFD_URL must be an absolute HTTP(S) InfiniDysk 2.x URL without credentials, query or fragment, and IFD_API_KEY must be its Admin API key")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, key, nil
}

func (monitor *ifdMonitor) get(ctx context.Context) ([]byte, error) {
	endpoint := *monitor.url
	endpoint.Path += "/api/v1/live"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Api-Key", monitor.key)
	request.Header.Set("Accept", "application/json")
	response, err := monitor.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("IFD 2.x live request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IFD 2.x live endpoint returned HTTP %d; check IFD_URL and the Admin API key", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if len(body) > maxResponseBytes {
		return nil, errors.New("IFD response exceeded 4 MiB")
	}
	return body, err
}

func (live ifdLive) files(now time.Time) ([]ifdRead, *float64, error) {
	sampledAt := time.UnixMilli(live.Timestamp)
	if live.Timestamp <= 0 || now.Sub(sampledAt) > 15*time.Second || sampledAt.Sub(now) > 15*time.Second {
		return nil, nil, errors.New("IFD 2.x live sample is missing, stale or ahead of the local clock")
	}
	if live.Fetched == nil || live.Served == nil || live.Providers == nil || live.Reads == nil || len(live.Reads) > 1000 {
		return nil, nil, errors.New("IFD 2.x returned invalid live statistics")
	}
	connections := new(float64)
	for _, provider := range live.Providers {
		if provider.Active == nil {
			return nil, nil, errors.New("IFD 2.x returned invalid connection statistics")
		}
		*connections += float64(*provider.Active)
	}
	files := make([]ifdRead, 0, len(live.Reads))
	seen := make(map[uint64]bool, len(live.Reads))
	for _, read := range live.Reads {
		if read.ID == nil || seen[*read.ID] || read.Path == "" || read.Sources == nil {
			return nil, nil, errors.New("IFD 2.x returned an invalid active read")
		}
		seen[*read.ID] = true
		file := ifdRead{ID: strconv.FormatUint(*read.ID, 10), Name: path.Base(read.Path), Fetched: new(float64), Rate: new(float64)}
		// Sources count provider fetches, not bytes served from cache to the reader.
		for _, source := range read.Sources {
			if source.Bytes == nil || source.Rate == nil {
				return nil, nil, errors.New("IFD 2.x returned invalid per-file fetching statistics")
			}
			*file.Fetched += float64(*source.Bytes)
			*file.Rate += float64(*source.Rate)
		}
		files = append(files, file)
	}
	return files, connections, nil
}

func (monitor *ifdMonitor) sample(ctx context.Context) {
	body, err := monitor.get(ctx)
	var live ifdLive
	if err == nil {
		err = json.Unmarshal(body, &live)
	}
	now := time.Now()
	var files []ifdRead
	var connections *float64
	if err == nil {
		files, connections, err = live.files(now)
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		log.Printf("InfiniDysk monitoring: %v", err)
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	point := ifdSample{Timestamp: now.UnixMilli()}
	if err == nil {
		if monitor.data.Available && monitor.data.SampledAt == live.Timestamp {
			return
		}
		fetched, served := float64(*live.Fetched), float64(*live.Served)
		point.Fetched, point.Served = &fetched, &served
		monitor.data.SampledAt = live.Timestamp
	}
	monitor.data.Available, monitor.data.FilesAvailable = err == nil, err == nil
	monitor.data.Files, monitor.data.Connections = files, connections
	monitor.data.History = append(monitor.data.History, point)
	for len(monitor.data.History) > 0 && monitor.data.History[0].Timestamp < now.Add(-historyWindow).UnixMilli() {
		monitor.data.History = monitor.data.History[1:]
	}
}

func (monitor *ifdMonitor) run(ctx context.Context) {
	monitor.sample(ctx)
	ticker := time.NewTicker(ifdPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			monitor.sample(ctx)
		}
	}
}

func (a *application) ifdJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if a.ifd == nil {
		json.NewEncoder(w).Encode(ifdSnapshot{})
		return
	}
	a.ifd.mu.RLock()
	defer a.ifd.mu.RUnlock()
	snapshot := a.ifd.data
	snapshot.Configured = true
	snapshot.Available = snapshot.Available && time.Since(time.UnixMilli(snapshot.SampledAt)) < 15*time.Second
	snapshot.FilesAvailable = snapshot.FilesAvailable && snapshot.Available
	if !snapshot.Available {
		snapshot.Connections = nil
		snapshot.Files = nil
	}
	json.NewEncoder(w).Encode(snapshot)
}
