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
)

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
