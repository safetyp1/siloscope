package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func (app *application) runActivity(ctx context.Context) {
	app.pollActivity(ctx)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.pollActivity(ctx)
		}
	}
}

func (app *application) pollActivity(ctx context.Context) {
	var tasks sync.WaitGroup
	tasks.Add(1)
	go func() { defer tasks.Done(); app.pollNodeAlerts(ctx) }()
	for _, source := range []string{"Silo", "Plex"} {
		if source == "Plex" && app.config.plexURL == nil {
			continue
		}
		tasks.Add(1)
		go func(source string) {
			defer tasks.Done()
			var sessions []notificationSession
			var available bool
			if source == "Silo" {
				body, _, err := app.fetch(ctx, "sessions", "/api/v2/admin/sessions", app.config.cacheTTL)
				available = err == nil && json.Unmarshal(body, &sessions) == nil && sessions != nil
			} else {
				body, err := app.plexSessionData(ctx)
				var payload struct {
					Sessions []notificationSession `json:"sessions"`
				}
				available = err == nil && json.Unmarshal(body, &payload) == nil && payload.Sessions != nil
				sessions = payload.Sessions
			}
			now := time.Now()
			app.activity.observe(source, sessions, available, now)
			if app.notifications != nil {
				app.notifications.observeService(source, source, &available, now)
			}
			if available && app.notifications != nil {
				app.notifications.observeSessions(source, sessions, now)
			}
		}(source)
	}
	tasks.Wait()
	if err := app.activity.save(); err != nil {
		log.Printf("playback history could not be saved: %v", err)
	}
}

type playbackActivity struct {
	MediaItemID     string     `json:"media_item_id,omitempty"`
	Poster          string     `json:"poster_url,omitempty"`
	Series          string     `json:"series_name,omitempty"`
	Season          *int       `json:"season_number,omitempty"`
	Episode         *int       `json:"episode_number,omitempty"`
	EpisodeName     string     `json:"episode_name,omitempty"`
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	Title           string     `json:"title"`
	User            string     `json:"user"`
	Method          string     `json:"method"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	LastSeen        time.Time  `json:"last_seen"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	AlreadyActive   bool       `json:"already_active"`
	DurationSeconds int64      `json:"duration_seconds"`
}

type activityHistory struct {
	mu      sync.Mutex
	path    string
	entries []*playbackActivity
	active  map[string]map[string]*playbackActivity
	seen    map[string]time.Time
}

func (history *activityHistory) open(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	history.path = filepath.Join(directory, "history.json")
	history.entries = make([]*playbackActivity, 0)
	file, err := os.Open(history.path)
	if err == nil {
		raw, readErr := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(raw) > 8*1024*1024 || json.Unmarshal(raw, &history.entries) != nil || history.entries == nil {
			return errors.New("invalid playback history store")
		}
		retained := history.entries[:0]
		for _, entry := range history.entries {
			if entry == nil || entry.ID == "" || entry.StartedAt.IsZero() || entry.LastSeen.IsZero() || (entry.Status != "active" && entry.Status != "stopped" && entry.Status != "unknown") {
				return errors.New("invalid playback history entry")
			}
			if entry.Status == "stopped" {
				retained = append(retained, entry)
			}
		}
		history.entries = retained
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	history.prune(time.Now())
	return history.save()
}

func (history *activityHistory) save() error {
	history.mu.Lock()
	defer history.mu.Unlock()
	if history.path == "" {
		return nil
	}
	raw, err := json.Marshal(history.entries)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(history.path), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), history.path)
}

func activityMethod(session notificationSession) string {
	if session.transcoding() {
		return "Transcoding"
	}
	for _, method := range []string{session.Effective, session.Method} {
		switch strings.ToLower(strings.ReplaceAll(method, "_", "")) {
		case "directplay", "direct":
			return "Direct play"
		case "remux", "directstream":
			return "Remux"
		}
	}
	return "Unknown"
}

func activityText(value string) string {
	text := []rune(strings.TrimSpace(value))
	if len(text) > 180 {
		text = text[:180]
	}
	return string(text)
}

func (history *activityHistory) prune(now time.Time) {
	kept := history.entries[:0]
	for _, entry := range history.entries {
		if entry.Status == "unknown" || (entry.Status == "active" && now.Sub(entry.LastSeen) > 35*time.Second) {
			continue
		}
		if now.Sub(entry.LastSeen) <= 30*24*time.Hour {
			kept = append(kept, entry)
		}
	}
	if len(kept) > 200 {
		kept = kept[len(kept)-200:]
	}
	history.entries = kept
	retained := make(map[*playbackActivity]bool, len(kept))
	for _, entry := range kept {
		retained[entry] = true
	}
	for _, sessions := range history.active {
		for key, entry := range sessions {
			if !retained[entry] {
				delete(sessions, key)
			}
		}
	}
}

func (history *activityHistory) observe(source string, sessions []notificationSession, available bool, now time.Time) {
	history.mu.Lock()
	defer history.mu.Unlock()
	if history.active == nil {
		history.active = make(map[string]map[string]*playbackActivity)
		history.seen = make(map[string]time.Time)
	}
	history.prune(now)
	previous := history.active[source]
	baseline := history.seen[source].IsZero() || now.Sub(history.seen[source]) > 35*time.Second
	if baseline {
		previous = nil
		history.active[source] = nil
	}
	if !available {
		return
	}
	current := make(map[string]*playbackActivity)
	for _, session := range sessions {
		key := session.key()
		if key == "" || current[key] != nil {
			continue
		}
		entry := previous[key]
		if entry == nil {
			entry = &playbackActivity{ID: source + ":" + key + ":" + now.Format(time.RFC3339Nano), Source: source, StartedAt: now, AlreadyActive: baseline, Status: "active"}
			history.entries = append(history.entries, entry)
		}
		entry.Title = activityText(session.Title)
		if session.MediaItemID != "" {
			entry.MediaItemID = session.MediaItemID
		}
		entry.Series = activityText(session.Series)
		entry.Season, entry.Episode = session.Season, session.Episode
		entry.EpisodeName = activityText(session.EpisodeName)
		if entry.EpisodeName == "" && entry.Series != "" {
			entry.EpisodeName = activityText(session.Title)
		}
		if len(session.Poster) <= 2048 {
			entry.Poster = session.Poster
		}
		if session.Series != "" {
			entry.Title = activityText(session.Series + ": " + session.Title)
		}
		if entry.Title == "" {
			entry.Title = "Unknown title"
		}
		entry.User = activityText(session.Username)
		if entry.User == "" && source == "Silo" {
			entry.User = activityText(session.Profile)
		}
		entry.Method = activityMethod(session)
		entry.LastSeen = now
		entry.DurationSeconds = int64(now.Sub(entry.StartedAt).Seconds())
		current[key] = entry
	}
	for key, entry := range previous {
		if current[key] == nil {
			entry.Status = "stopped"
			ended := now
			entry.EndedAt = &ended
			entry.DurationSeconds = int64(now.Sub(entry.StartedAt).Seconds())
		}
	}
	history.active[source] = current
	history.seen[source] = now
	history.prune(now)
}

func (app *application) activityJSON(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	app.activity.mu.Lock()
	app.activity.prune(time.Now())
	entries := make([]playbackActivity, 0, len(app.activity.entries))
	for index := len(app.activity.entries) - 1; index >= 0; index-- {
		entry := *app.activity.entries[index]
		if app.config.ignoresUser(entry.User) {
			continue
		}
		entries = append(entries, entry)
	}
	app.activity.mu.Unlock()
	if err := app.refreshActivityPosters(request.Context(), entries); err != nil {
		log.Printf("playback history artwork could not be refreshed: %v", err)
	}
	_ = json.NewEncoder(writer).Encode(entries)
}

func activityPosterItemID(entry playbackActivity) string {
	if entry.MediaItemID != "" {
		return entry.MediaItemID
	}
	target, err := url.Parse(entry.Poster)
	if err != nil {
		return ""
	}
	if target.Path == "/api/silo/poster" {
		target, err = url.Parse(target.Query().Get("path"))
		if err != nil {
			return ""
		}
	}
	if id, ok := strings.CutPrefix(target.Path, "/api/v2/item-posters/"); ok && id != "" && !strings.Contains(id, "/") {
		return id
	}
	return ""
}

func (app *application) refreshActivityPosters(ctx context.Context, entries []playbackActivity) error {
	query := url.Values{"limit": {"200"}}
	ids := make(map[string]bool)
	for _, entry := range entries {
		if entry.Source == "Silo" {
			if id := activityPosterItemID(entry); id != "" && !ids[id] {
				ids[id] = true
				query.Add("content_ids", id)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	body, _, err := app.fetch(ctx, "activity-posters:"+query.Encode(), "/api/v2/admin/catalog/items?"+query.Encode(), time.Minute)
	if err != nil {
		return err
	}
	var collection struct {
		Items []struct {
			ID     string `json:"content_id"`
			Poster string `json:"poster_url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &collection); err != nil {
		return err
	}
	if collection.Items == nil {
		return errors.New("Silo returned an invalid history artwork collection")
	}
	posters := make(map[string]string, len(collection.Items))
	for _, item := range collection.Items {
		if ids[item.ID] {
			posters[item.ID] = item.Poster
		}
	}
	for index := range entries {
		if entries[index].Source != "Silo" {
			continue
		}
		if id := activityPosterItemID(entries[index]); ids[id] {
			poster, err := app.siloPosterURL(posters[id])
			if err != nil {
				return err
			}
			entries[index].Poster = poster
		}
	}
	return nil
}
