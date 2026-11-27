package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestActivityRefreshesExpiredSiloPosters(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v2/admin/catalog/items" || r.Header.Get("Authorization") != "Bearer test-key" || r.URL.Query().Get("limit") != "200" {
			t.Errorf("incorrect catalog request: %s", r.URL)
		}
		ids := r.URL.Query()["content_ids"]
		if len(ids) != 2 {
			t.Errorf("expected two deduplicated item IDs, got %v", ids)
		}
		fmt.Fprint(w, `{"items":[{"content_id":"movie-one","poster_url":"/api/v2/item-posters/movie-one?exp=9999999999&sig=fresh"},{"content_id":"episode-one","poster_url":"/api/v2/artwork/series/poster.webp?exp=9999999999&sig=fresh"}]}`)
	}))
	defer upstream.Close()
	endpoint, _ := url.Parse(upstream.URL)
	app := newApplication(config{siloURL: endpoint, apiKey: "test-key", timeout: defaultTimeout})
	now := time.Now()
	expired := "/api/silo/poster?" + url.Values{"path": {"/api/v2/item-posters/movie-one?exp=1&sig=expired"}}.Encode()
	app.activity.entries = []*playbackActivity{
		{ID: "movie", Source: "Silo", Poster: expired, Status: "stopped", StartedAt: now, LastSeen: now},
		{ID: "repeat", Source: "Silo", MediaItemID: "movie-one", Poster: expired, Status: "stopped", StartedAt: now, LastSeen: now},
		{ID: "show", Source: "Silo", MediaItemID: "episode-one", Poster: "/api/silo/poster?path=expired", Status: "stopped", StartedAt: now, LastSeen: now},
		{ID: "plex", Source: "Plex", Poster: "/api/plex/poster?id=42", Status: "stopped", StartedAt: now, LastSeen: now},
	}
	for range 2 {
		recorder := httptest.NewRecorder()
		app.activityJSON(recorder, httptest.NewRequest(http.MethodGet, "/api/activity", nil))
		var entries []playbackActivity
		if err := json.Unmarshal(recorder.Body.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Source == "Plex" {
				if entry.Poster != "/api/plex/poster?id=42" {
					t.Fatal("Plex poster changed")
				}
				continue
			}
			poster, err := url.Parse(entry.Poster)
			if err != nil || poster.Path != "/api/silo/poster" || !strings.Contains(poster.Query().Get("path"), "sig=fresh") {
				t.Fatalf("history retained an expired poster: %+v", entry)
			}
		}
	}
	if requests != 1 {
		t.Fatalf("catalog refresh was not cached: %d", requests)
	}
	if app.activity.entries[0].Poster != expired {
		t.Fatal("rendering changed the stored history")
	}
}

func TestActivityPersistsSiloMediaID(t *testing.T) {
	directory := t.TempDir()
	history := &activityHistory{}
	if err := history.open(directory); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	history.observe("Silo", []notificationSession{{SessionID: "one", MediaItemID: "episode-one", Title: "Pilot"}}, true, now)
	history.observe("Silo", nil, true, now.Add(time.Second))
	if err := history.save(); err != nil {
		t.Fatal(err)
	}
	reloaded := &activityHistory{}
	if err := reloaded.open(directory); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.entries) != 1 || reloaded.entries[0].MediaItemID != "episode-one" {
		t.Fatal("media identity did not survive a restart")
	}
}

func TestActivityArtworkFailurePreservesHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	app := newApplication(config{siloURL: endpoint, timeout: defaultTimeout})
	entries := []playbackActivity{{Source: "Silo", MediaItemID: "movie-one", Poster: "old-poster"}}
	if err := app.refreshActivityPosters(context.Background(), entries); err == nil {
		t.Fatal("artwork refresh failure was hidden")
	}
	if entries[0].Poster != "old-poster" {
		t.Fatal("artwork outage discarded stored poster")
	}
}
func TestSiloSessionCompatibility(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprint(current), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/admin/sessions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("unexpected sessions request: %s", r.URL.Path)
				}
				if current {
					fmt.Fprint(w, `{"items":[{"session_id":"one","media_title":"Pilot","series_title":"Show","season_number":1,"episode_number":2,"poster_url":"/api/v2/item-posters/show?expires=123&sig=a%2Bb","subtitle":{"language":"en"},"position_seconds":42}],"page":{"has_more":false}}`)
				} else {
					fmt.Fprint(w, `{"items":[{"session_id":"one","media_title":"Show","series_name":"Show","episode_name":"Pilot","season_number":1,"episode_number":2,"poster_url":"https://images.example/poster.jpg","subtitle":"Legacy subtitle","position_seconds":42}],"page":{"has_more":false}}`)
				}
			}))
			defer server.Close()
			endpoint, _ := url.Parse(server.URL)
			app := newApplication(config{siloURL: endpoint, apiKey: "test-key", timeout: defaultTimeout})
			body, _, err := app.fetch(context.Background(), "sessions", "/api/v2/admin/sessions", defaultCacheTTL)
			if err != nil {
				t.Fatal(err)
			}
			var sessions []notificationSession
			if err := json.Unmarshal(body, &sessions); err != nil {
				t.Fatal(err)
			}
			session := sessions[0]
			if session.Series != "Show" || session.EpisodeName != "Pilot" || *session.Season != 1 || *session.Episode != 2 {
				t.Fatalf("incorrect episode: %+v", session)
			}
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(body, &records); err != nil {
				t.Fatal(err)
			}
			if string(records[0]["position_seconds"]) != "42" {
				t.Fatal("lost playback fields")
			}
			if current {
				if session.Title != "Pilot" {
					t.Fatalf("incorrect episode title: %s", session.Title)
				}
				poster, _ := url.Parse(session.Poster)
				if poster.Path != "/api/silo/poster" || poster.Query().Get("path") != "/api/v2/item-posters/show?expires=123&sig=a%2Bb" {
					t.Fatalf("incorrect signed poster: %s", session.Poster)
				}
				if _, ok := records[0]["subtitle"]; ok {
					t.Fatal("track object exposed as display subtitle")
				}
			} else if session.Poster != "https://images.example/poster.jpg" || string(records[0]["subtitle"]) != `"Legacy subtitle"` {
				t.Fatal("legacy display changed")
			}
		})
	}
}

func TestSiloSessionsWithoutEpisodeMetadata(t *testing.T) {
	endpoint, _ := url.Parse("http://silo:8080")
	app := newApplication(config{siloURL: endpoint})
	for _, fixture := range []string{`[]`, `[{"media_title":"Movie","media_type":"movie","subtitle":{"language":"en"}}]`} {
		body, err := app.normalizeSiloSessions([]byte(fixture))
		if err != nil {
			t.Fatal(err)
		}
		if fixture == `[]` {
			if string(body) != `[]` {
				t.Fatalf("empty sessions changed: %s", body)
			}
			continue
		}
		var sessions []notificationSession
		if err := json.Unmarshal(body, &sessions); err != nil {
			t.Fatal(err)
		}
		if sessions[0].Title != "Movie" || sessions[0].Series != "" || sessions[0].Poster != "" || strings.Contains(string(body), `"subtitle"`) {
			t.Fatalf("incorrect movie metadata: %s", body)
		}
	}
}

func TestSiloPosterProxy(t *testing.T) {
	for _, path := range []string{"/api/v2/artwork/poster.webp", "/api/v2/item-posters/show"} {
		t.Run(path, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("admin key sent to artwork")
				}
				if r.URL.Path == "/storage/poster" {
					w.Header().Set("Content-Type", "image/webp")
					fmt.Fprint(w, "image bytes")
					return
				}
				if r.URL.Path != path || r.URL.RawQuery != "sig=a%2Bb&expires=123" {
					t.Errorf("signed URL changed: %s", r.URL)
				}
				http.Redirect(w, r, "/storage/poster", http.StatusFound)
			}))
			defer server.Close()
			endpoint, _ := url.Parse(server.URL)
			app := newApplication(config{siloURL: endpoint, apiKey: "secret", timeout: defaultTimeout})
			raw := path + "?sig=a%2Bb&expires=123"
			// Absolute server-issued links also need to work across Docker networks.
			body, err := app.normalizeSiloSessions([]byte(fmt.Sprintf(`[{"poster_url":%q}]`, server.URL+raw)))
			if err != nil {
				t.Fatal(err)
			}
			var sessions []notificationSession
			if err := json.Unmarshal(body, &sessions); err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, sessions[0].Poster, nil))
			if recorder.Code != http.StatusOK || recorder.Body.String() != "image bytes" || recorder.Header().Get("Content-Type") != "image/webp" {
				t.Fatalf("poster not served: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSiloPosterRejectsInvalidTargetsAndResponses(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Query().Get("failure") {
		case "expired":
			http.NotFound(w, r)
		case "oversized":
			w.Header().Set("Content-Type", "image/jpeg")
			fmt.Fprint(w, strings.Repeat("x", maxResponseBytes+1))
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>login</html>")
		}
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	app := newApplication(config{siloURL: endpoint, timeout: defaultTimeout})
	for _, target := range []string{"https://other.example/api/v2/artwork/x", "//other.example/api/v2/artwork/x", "/api/v2/admin/sessions", "/api/v2/artwork/../admin/sessions", "/api/v2/artwork/%2e%2e/admin/sessions", "/api/v2/artwork/x#fragment"} {
		recorder := httptest.NewRecorder()
		app.siloPoster(recorder, httptest.NewRequest(http.MethodGet, "/api/silo/poster?"+url.Values{"path": {target}}.Encode(), nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid target %s: %d", target, recorder.Code)
		}
	}
	if requests != 0 {
		t.Fatal("invalid targets reached upstream")
	}
	for _, failure := range []string{"expired", "oversized", "html"} {
		recorder := httptest.NewRecorder()
		target := "/api/v2/artwork/x?failure=" + failure
		app.siloPoster(recorder, httptest.NewRequest(http.MethodGet, "/api/silo/poster?"+url.Values{"path": {target}}.Encode(), nil))
		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("accepted %s response: %d", failure, recorder.Code)
		}
	}
}

func TestIgnoreUsersConfig(t *testing.T) {
	t.Setenv("SILO_URL", "http://silo:8080")
	t.Setenv("SILO_API_KEY", "test-key")
	for _, raw := range []string{"", " Alex ", " Alex, BOB, ,alex,"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("IGNORE_USERS", raw)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ignoresUser(" aLeX ") != (raw != "") || cfg.ignoresUser("bob") != strings.Contains(raw, "BOB") {
				t.Fatal("username list was not normalized")
			}
			if cfg.ignoresUser("") || cfg.ignoresUser("Alexander") || cfg.ignoresUser("Carol") {
				t.Fatal("matched an empty, partial, or unlisted username")
			}
		})
	}
}

func TestIgnoreUsersSiloPlaybackAndHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/base/api/v2/admin/sessions" {
			fmt.Fprint(writer, `{"items":[{"session_id":"ignored","username":" ALEX ","media_title":"Hidden"},{"session_id":"visible","username":"Carol","media_title":"Visible"},{"session_id":"anonymous","media_title":"Anonymous"}]}`)
			return
		}
		fmt.Fprint(writer, `{"items":[]}`)
	}))
	defer upstream.Close()
	endpoint, _ := url.Parse(upstream.URL + "/base")
	app := newApplication(config{siloURL: endpoint, apiKey: "test-key", timeout: defaultTimeout, cacheTTL: defaultCacheTTL})
	app.config.ignoredUsers = parseIgnoredUsers("alex,bob")
	for range 2 {
		response := httptest.NewRecorder()
		app.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
		var sessions []notificationSession
		if err := json.Unmarshal(response.Body.Bytes(), &sessions); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || len(sessions) != 2 || strings.Contains(response.Body.String(), "Hidden") {
			t.Fatalf("ignored session remained in playback: %s", response.Body)
		}
	}
	app.pollActivity(context.Background())
	if len(app.activity.entries) != 2 {
		t.Fatalf("expected two history entries, got %d", len(app.activity.entries))
	}
	for _, entry := range app.activity.entries {
		if app.config.ignoresUser(entry.User) {
			t.Fatal("ignored session was recorded")
		}
	}
}

func TestIgnoreUsersPlexPlaybackAndHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `<MediaContainer><Video sessionKey="1" title="Hidden"><User title="Alex"/><Player state="playing"/></Video><Track sessionKey="2" title="Hidden song"><User title="BOB"/><Player state="playing"/></Track><Video sessionKey="3" title="Visible"><User title="Carol"/><Player state="playing"/></Video></MediaContainer>`)
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	silo := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"items":[]}`)
	}))
	defer silo.Close()
	endpoint, _ := url.Parse(silo.URL)
	app := newApplication(config{siloURL: endpoint, apiKey: "test-key", timeout: defaultTimeout, cacheTTL: defaultCacheTTL})
	app.config.plexURL = parsed
	app.config.plexToken = "test-token"
	app.config.ignoredUsers = parseIgnoredUsers(" alex, bob ")
	for range 2 {
		response := httptest.NewRecorder()
		app.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/plex/sessions", nil))
		var payload struct {
			Enabled  bool           `json:"enabled"`
			Sessions []plexPlayback `json:"sessions"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || !payload.Enabled || len(payload.Sessions) != 1 || payload.Sessions[0].Username != "Carol" {
			t.Fatalf("ignored session remained in playback: %s", response.Body)
		}
	}
	app.pollActivity(context.Background())
	if len(app.activity.entries) != 1 || app.activity.entries[0].User != "Carol" {
		t.Fatal("ignored Plex session was recorded")
	}
}

func TestIgnoreUsersSavedHistory(t *testing.T) {
	directory := t.TempDir()
	var history activityHistory
	if err := history.open(directory); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	for _, source := range []string{"Silo", "Plex"} {
		history.observe(source, []notificationSession{{SessionID: "hidden", Username: "Alex"}, {SessionID: "visible", Username: "Carol"}}, true, now)
		history.observe(source, nil, true, now.Add(time.Second))
	}
	if err := history.save(); err != nil {
		t.Fatal(err)
	}
	app := newApplication(config{ignoredUsers: parseIgnoredUsers(" ALEX ")})
	if err := app.activity.open(directory); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []int{2, 4} {
		response := httptest.NewRecorder()
		app.activityJSON(response, httptest.NewRequest(http.MethodGet, "/api/activity", nil))
		var entries []playbackActivity
		if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != expected || len(app.activity.entries) != 4 {
			t.Fatal("saved history was not filtered non-destructively")
		}
		if expected == 2 {
			for _, entry := range entries {
				if entry.User != "Carol" {
					t.Fatal("ignored saved history remained visible")
				}
			}
		}
		app.config.ignoredUsers = nil
	}
}
