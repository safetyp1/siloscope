package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type plexMediaContainer struct {
	XMLName xml.Name       `xml:"MediaContainer"`
	Videos  []plexMetadata `xml:"Video"`
	Tracks  []plexMetadata `xml:"Track"`
}

type plexMetadata struct {
	Session struct {
		ID string `xml:"id,attr"`
	} `xml:"Session"`
	Key                  string  `xml:"sessionKey,attr"`
	RatingKey            string  `xml:"ratingKey,attr"`
	GrandparentRatingKey string  `xml:"grandparentRatingKey,attr"`
	Type                 string  `xml:"type,attr"`
	Title                string  `xml:"title,attr"`
	GrandparentTitle     string  `xml:"grandparentTitle,attr"`
	ParentTitle          string  `xml:"parentTitle,attr"`
	Index                *int    `xml:"index,attr"`
	ParentIndex          *int    `xml:"parentIndex,attr"`
	Duration             float64 `xml:"duration,attr"`
	ViewOffset           float64 `xml:"viewOffset,attr"`
	User                 struct {
		Title string `xml:"title,attr"`
	} `xml:"User"`
	Player struct {
		Title   string `xml:"title,attr"`
		Product string `xml:"product,attr"`
		State   string `xml:"state,attr"`
	} `xml:"Player"`
	Transcode struct {
		VideoDecision string `xml:"videoDecision,attr"`
		AudioDecision string `xml:"audioDecision,attr"`
		AudioCodec    string `xml:"audioCodec,attr"`
		Width         int    `xml:"width,attr"`
		Height        int    `xml:"height,attr"`
	} `xml:"TranscodeSession"`
	Media []struct {
		Bitrate         string `xml:"bitrate,attr"`
		Selected        string `xml:"selected,attr"`
		VideoResolution string `xml:"videoResolution,attr"`
		VideoDecision   string `xml:"videoDecision,attr"`
		AudioDecision   string `xml:"audioDecision,attr"`
		AudioCodec      string `xml:"audioCodec,attr"`
		Parts           []struct {
			Decision string `xml:"decision,attr"`
			Streams  []struct {
				Type     int    `xml:"streamType,attr"`
				Decision string `xml:"decision,attr"`
				Selected string `xml:"selected,attr"`
				Codec    string `xml:"codec,attr"`
				Profile  string `xml:"profile,attr"`
			} `xml:"Stream"`
		} `xml:"Part"`
	} `xml:"Media"`
}

type plexPlayback struct {
	SourceBitrateKbps int64   `json:"source_bitrate_kbps,omitempty"`
	TerminationID     string  `json:"termination_id,omitempty"`
	ID                string  `json:"id"`
	Source            string  `json:"source"`
	Title             string  `json:"media_title"`
	MediaType         string  `json:"media_type"`
	Series            string  `json:"series_name,omitempty"`
	Season            *int    `json:"season_number,omitempty"`
	Episode           *int    `json:"episode_number,omitempty"`
	EpisodeName       string  `json:"episode_name,omitempty"`
	Subtitle          string  `json:"subtitle,omitempty"`
	Username          string  `json:"username"`
	Client            string  `json:"client_name"`
	Method            string  `json:"play_method"`
	Profile           string  `json:"profile_name,omitempty"`
	Resolution        string  `json:"source_video_resolution,omitempty"`
	TargetResolution  string  `json:"target_resolution,omitempty"`
	AudioCodec        string  `json:"source_audio_codec,omitempty"`
	AudioProfile      string  `json:"source_audio_profile,omitempty"`
	TargetAudioCodec  string  `json:"target_audio_codec,omitempty"`
	Paused            bool    `json:"is_paused"`
	PlaybackState     string  `json:"playback_state"`
	Duration          float64 `json:"file_duration"`
	Position          float64 `json:"position_seconds"`
	Poster            string  `json:"poster_url,omitempty"`
}

func loadPlexConfig() (*url.URL, string, error) {
	rawURL := strings.TrimSpace(os.Getenv("PLEX_URL"))
	token := strings.TrimSpace(os.Getenv("PLEX_TOKEN"))
	if rawURL == "" && token == "" {
		return nil, "", nil
	}
	if rawURL == "" || token == "" {
		return nil, "", errors.New("PLEX_URL and PLEX_TOKEN must be set together")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "", errors.New("PLEX_URL must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, "", errors.New("PLEX_URL must not contain credentials, a query, or a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, token, nil
}

func (app *application) plexGet(ctx context.Context, path, accept string) (*http.Response, error) {
	upstream := *app.config.plexURL
	upstream.Path = strings.TrimRight(upstream.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Plex-Token", app.config.plexToken)
	request.Header.Set("Accept", accept)
	client := *app.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(request)
}

func normalizePlexSession(metadata plexMetadata) plexPlayback {
	method := "direct_play"
	video, audio := metadata.Transcode.VideoDecision, metadata.Transcode.AudioDecision
	profile := ""
	audioCodec, audioProfile := "", ""
	var sourceBitrate int64
	if len(metadata.Media) > 0 {
		media := metadata.Media[0]
		for _, candidate := range metadata.Media {
			if candidate.Selected == "1" {
				media = candidate
				break
			}
		}
		profile = media.VideoResolution
		if bitrate, err := strconv.ParseInt(media.Bitrate, 10, 64); err == nil && bitrate > 0 {
			sourceBitrate = bitrate
		}
		audioCodec = media.AudioCodec
		if video == "" {
			video = media.VideoDecision
		}
		if audio == "" {
			audio = media.AudioDecision
		}
		for _, part := range media.Parts {
			if part.Decision == "transcode" || part.Decision == "copy" {
				method = "remux"
			}
			for _, stream := range part.Streams {
				if stream.Type == 2 && stream.Selected == "1" {
					audioCodec, audioProfile = stream.Codec, stream.Profile
				}
				if stream.Type == 1 && video == "" {
					video = stream.Decision
				}
				if stream.Type == 2 && audio == "" {
					audio = stream.Decision
				}
			}
		}
	}
	if video == "transcode" {
		method = "transcode"
	} else if audio == "transcode" {
		method = "audio"
	} else if video == "copy" || audio == "copy" {
		method = "remux"
	}
	session := plexPlayback{
		SourceBitrateKbps: sourceBitrate,
		ID:                "plex:" + metadata.Key, Source: "plex", Title: metadata.Title, MediaType: metadata.Type,
		Username: metadata.User.Title, Client: metadata.Player.Title, Method: method,
		Resolution: profile, AudioCodec: audioCodec, AudioProfile: audioProfile,
		Paused: metadata.Player.State == "paused", PlaybackState: metadata.Player.State,
		Duration: metadata.Duration / 1000, Position: metadata.ViewOffset / 1000,
	}
	if video == "transcode" && metadata.Transcode.Width > 0 && metadata.Transcode.Height > 0 {
		session.TargetResolution = strconv.Itoa(metadata.Transcode.Width) + "x" + strconv.Itoa(metadata.Transcode.Height)
	}
	if audio == "transcode" {
		session.TargetAudioCodec = metadata.Transcode.AudioCodec
	}
	if session.Client == "" {
		session.Client = metadata.Player.Product
	}
	posterKey := metadata.RatingKey
	if metadata.Type == "episode" {
		session.Series, session.Season, session.Episode, session.EpisodeName = metadata.GrandparentTitle, metadata.ParentIndex, metadata.Index, metadata.Title
		if metadata.GrandparentRatingKey != "" {
			posterKey = metadata.GrandparentRatingKey
		}
	} else if metadata.Type == "track" {
		session.Subtitle = strings.Trim(strings.Join([]string{metadata.GrandparentTitle, metadata.ParentTitle}, " / "), " / ")
	}
	if _, err := strconv.ParseUint(posterKey, 10, 64); err == nil && posterKey != "0" {
		session.Poster = "/api/plex/poster?id=" + posterKey
	}
	session.TerminationID = metadata.Session.ID
	return session
}

func (app *application) plexSessionsJSON(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	body, err := app.plexSessionData(request.Context())
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "Plex is unavailable")
		return
	}
	_, _ = writer.Write(body)
}

func (app *application) plexSessionData(ctx context.Context) ([]byte, error) {
	if app.config.plexURL == nil {
		return []byte(`{"enabled":false,"sessions":[]}`), nil
	}
	app.cache.mu.Lock()
	cached, exists := app.cache.entries["plex-sessions"]
	app.cache.mu.Unlock()
	if exists && time.Now().Before(cached.expiresAt) {
		return cached.body, nil
	}
	response, err := app.plexGet(ctx, "/status/sessions", "application/xml")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Plex returned %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, errors.New("invalid Plex response size")
	}
	var container plexMediaContainer
	if err := xml.Unmarshal(body, &container); err != nil {
		return nil, err
	}
	sessions := make([]plexPlayback, 0, len(container.Videos)+len(container.Tracks))
	for _, metadata := range append(container.Videos, container.Tracks...) {
		if metadata.Player.State != "stopped" {
			sessions = append(sessions, normalizePlexSession(metadata))
		}
	}
	body, err = json.Marshal(struct {
		Enabled  bool           `json:"enabled"`
		Sessions []plexPlayback `json:"sessions"`
	}{true, sessions})
	if err != nil {
		return nil, err
	}
	app.cache.mu.Lock()
	app.cache.entries["plex-sessions"] = cacheEntry{body: body, expiresAt: time.Now().Add(app.config.cacheTTL)}
	app.cache.mu.Unlock()
	return body, nil
}

func (app *application) plexPoster(writer http.ResponseWriter, request *http.Request) {
	if app.config.plexURL == nil {
		http.NotFound(writer, request)
		return
	}
	identifier, err := strconv.ParseUint(request.URL.Query().Get("id"), 10, 64)
	if err != nil || identifier == 0 {
		writeJSONError(writer, http.StatusBadRequest, "Invalid poster ID")
		return
	}
	response, err := app.plexGet(request.Context(), "/library/metadata/"+strconv.FormatUint(identifier, 10)+"/thumb", "image/jpeg, image/png, image/webp")
	if err != nil {
		writeJSONError(writer, http.StatusBadGateway, "Plex poster unavailable")
		return
	}
	defer response.Body.Close()
	contentType := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	if response.StatusCode != http.StatusOK || (contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp") {
		writeJSONError(writer, http.StatusBadGateway, "Plex poster unavailable")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		writeJSONError(writer, http.StatusBadGateway, "Plex poster unavailable")
		return
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = writer.Write(body)
}
