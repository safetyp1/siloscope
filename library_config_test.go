package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestLibraryConfig(t *testing.T) {
	t.Setenv("SILO_URL", "http://silo:8080")
	t.Setenv("SILO_API_KEY", "test-key")
	for _, key := range []string{"PLEX_URL", "PLEX_TOKEN", "IFD_URL", "IFD_API_KEY", "RADARR_URL", "RADARR_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("SONARR_URL", "not a URL")
	t.Setenv("SONARR_API_KEY", "")
	t.Setenv("ARR_REPLACEMENTS_ENABLED", "true")
	for _, setting := range []string{"", "false", "true", "invalid"} {
		t.Setenv("LIBRARY_ENABLED", setting)
		cfg, err := loadConfig()
		if setting == "true" || setting == "invalid" {
			if err == nil {
				t.Fatalf("accepted invalid enabled config for %q", setting)
			}
			continue
		}
		if err != nil || cfg.libraryEnabled || len(cfg.arrConfigs) != 0 {
			t.Fatalf("disabled Library did not ignore Arr config: %+v, %v", cfg, err)
		}
	}
	t.Setenv("LIBRARY_ENABLED", "true")
	t.Setenv("SONARR_URL", "http://sonarr:8989")
	t.Setenv("SONARR_API_KEY", "test-key")
	cfg, err := loadConfig()
	if err != nil || !cfg.libraryEnabled || len(cfg.arrConfigs) != 1 {
		t.Fatalf("valid enabled Library config failed: %+v, %v", cfg, err)
	}
}

func TestLibraryRoutesAndPage(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			endpoint, _ := url.Parse("http://silo:8080")
			app := newApplication(config{siloURL: endpoint, libraryEnabled: enabled, timeout: defaultTimeout})
			handler := app.routes()
			page := httptest.NewRecorder()
			handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-library-enabled="`+strconv.FormatBool(enabled)+`"`) || page.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("incorrect page config: %d %s", page.Code, page.Header())
			}
			if enabled {
				result := httptest.NewRecorder()
				handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/api/library", nil))
				var config struct {
					Sources []string `json:"sources"`
				}
				if err := json.Unmarshal(result.Body.Bytes(), &config); err != nil || result.Code != http.StatusOK || config.Sources == nil {
					t.Fatalf("enabled API unavailable: %d %s", result.Code, result.Body.String())
				}
				return
			}
			for _, route := range []struct{ method, path string }{
				{http.MethodGet, "/api/library"},
				{http.MethodGet, "/api/library/items?source=sonarr"},
				{http.MethodPost, "/api/library/preview"},
				{http.MethodPost, "/api/library/replace"},
				{http.MethodGet, "/api/library/operations"},
				{http.MethodGet, "/api/library/operations/one"},
				{http.MethodPost, "/api/library/retry-search"},
			} {
				result := httptest.NewRecorder()
				handler.ServeHTTP(result, httptest.NewRequest(route.method, route.path, nil))
				if result.Code != http.StatusNotFound || !strings.Contains(result.Body.String(), "Library is disabled") {
					t.Fatalf("disabled route %s remains accessible: %d %s", route.path, result.Code, result.Body.String())
				}
			}
		})
	}
}
