package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

func siloPosterPath(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil || target.IsAbs() || target.Host != "" || target.User != nil || target.Fragment != "" {
		return nil, errors.New("invalid Silo poster URL")
	}
	if !strings.HasPrefix(target.Path, "/api/v2/artwork/") && !strings.HasPrefix(target.Path, "/api/v2/item-posters/") {
		return nil, errors.New("invalid Silo poster path")
	}
	for _, segment := range strings.Split(target.Path, "/") {
		if segment == "." || segment == ".." || strings.Contains(segment, `\`) {
			return nil, errors.New("invalid Silo poster path")
		}
	}
	return target, nil
}

func (a *application) normalizeSiloSessions(body []byte) ([]byte, error) {
	var sessions []map[string]json.RawMessage
	if err := json.Unmarshal(body, &sessions); err != nil {
		return nil, fmt.Errorf("decode Silo sessions: %w", err)
	}
	for _, session := range sessions {
		if series, ok := session["series_title"]; ok {
			session["series_name"] = series
			session["episode_name"] = session["media_title"]
		}
		// Silo's subtitle is now track metadata, not the legacy display string.
		if subtitle := session["subtitle"]; len(subtitle) > 0 && subtitle[0] == '{' {
			delete(session, "subtitle")
		}
		var poster string
		if raw := session["poster_url"]; raw != nil {
			if err := json.Unmarshal(raw, &poster); err != nil {
				return nil, fmt.Errorf("decode Silo poster URL: %w", err)
			}
		}
		if poster == "" {
			continue
		}
		poster, err := a.siloPosterURL(poster)
		if err != nil {
			return nil, err
		}
		session["poster_url"], err = json.Marshal(poster)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(sessions)
}

func (a *application) siloPosterURL(poster string) (string, error) {
	if poster == "" {
		return "", nil
	}
	target, err := url.Parse(poster)
	if err != nil {
		return "", fmt.Errorf("parse Silo poster URL: %w", err)
	}
	if (!target.IsAbs() && target.Host == "") || (target.Scheme == a.config.siloURL.Scheme && target.Host == a.config.siloURL.Host) {
		if _, err := siloPosterPath(target.RequestURI()); err == nil {
			return "/api/silo/poster?" + url.Values{"path": {target.RequestURI()}}.Encode(), nil
		}
		if !target.IsAbs() {
			return a.config.siloURL.ResolveReference(target).String(), nil
		}
	}
	return poster, nil
}
func (a *application) siloPoster(writer http.ResponseWriter, request *http.Request) {
	target, err := siloPosterPath(request.URL.Query().Get("path"))
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "Invalid Silo poster URL")
		return
	}
	upstream := a.config.siloURL.ResolveReference(target)
	posterRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, upstream.String(), nil)
	if err != nil {
		log.Printf("Silo poster request: %v", err)
		writeJSONError(writer, http.StatusBadGateway, "Silo poster unavailable")
		return
	}
	posterRequest.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	// These links are signed and may redirect to storage; never send the admin key.
	client := *a.client
	client.CheckRedirect = nil
	response, err := client.Do(posterRequest)
	if err != nil {
		log.Printf("Silo poster request: %v", err)
		writeJSONError(writer, http.StatusBadGateway, "Silo poster unavailable")
		return
	}
	defer response.Body.Close()
	contentType := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	if response.StatusCode != http.StatusOK || (contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp") {
		log.Printf("Silo poster returned status %d, content type %q", response.StatusCode, contentType)
		writeJSONError(writer, http.StatusBadGateway, "Silo poster unavailable")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		log.Printf("Silo poster read failed or exceeded 4 MiB: %v", err)
		writeJSONError(writer, http.StatusBadGateway, "Silo poster unavailable")
		return
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = writer.Write(body)
}
