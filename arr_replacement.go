package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type arrFile struct {
	ID           int64  `json:"id"`
	MovieID      int64  `json:"movieId"`
	SeriesID     int64  `json:"seriesId"`
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	Added        string `json:"dateAdded"`
	Quality      struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
}

type replacementTarget struct {
	Source      string `json:"source"`
	ItemID      int64  `json:"item_id"`
	FileID      int64  `json:"file_id"`
	Title       string `json:"title"`
	Filename    string `json:"filename"`
	Quality     string `json:"quality"`
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint"`
}

type replacementPreview struct {
	Token   string            `json:"token"`
	Expires time.Time         `json:"expires"`
	Target  replacementTarget `json:"target"`
}

type replacementJob struct {
	ID         string            `json:"id"`
	Target     replacementTarget `json:"target"`
	State      string            `json:"state"`
	Message    string            `json:"message"`
	CommandID  int64             `json:"command_id,omitempty"`
	Created    time.Time         `json:"created"`
	Updated    time.Time         `json:"updated"`
	Connection string            `json:"connection"`
	Checked    time.Time         `json:"-"`
}

func (client *arrClient) connectionID() string {
	digest := sha256.Sum256([]byte(client.config.URL.String()))
	return hex.EncodeToString(digest[:])
}

func (service *arrService) open(directory string) error {
	if directory == "" {
		return errors.New("REPLACEMENT_DATA_DIR is required when ARR_REPLACEMENTS_ENABLED is true")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errors.New("replacement storage could not be created")
	}
	service.directory = directory
	file, err := os.Open(filepath.Join(directory, "operations.json"))
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxResponseBytes+1))
		if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &service.jobs) != nil || service.jobs == nil || len(service.jobs) > 100 {
			return errors.New("invalid replacement journal")
		}
		for id, job := range service.jobs {
			if job == nil || id != job.ID || len(id) != 32 || (job.Target.Source != "radarr" && job.Target.Source != "sonarr") {
				return errors.New("invalid replacement journal entry")
			}
			if job.State == "preparing" || job.State == "deleting" || job.State == "deleted" || job.State == "searching" {
				job.State, job.Message = "uncertain", "Interrupted operation. Check the file and queue in Sonarr/Radarr before taking further action."
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("replacement storage could not be opened")
	}
	if err := service.saveLocked(); err != nil {
		return err
	}
	service.enabled = true
	return nil
}

func (service *arrService) saveLocked() error {
	raw, err := json.Marshal(service.jobs)
	if err != nil || len(raw) > maxResponseBytes {
		return errors.New("replacement journal limit reached")
	}
	file, err := os.CreateTemp(service.directory, ".operations-*")
	if err != nil {
		return errors.New("replacement journal could not be saved")
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(service.directory, "operations.json"))
	}
	if err == nil {
		directory, openErr := os.Open(service.directory)
		if openErr != nil {
			err = openErr
		} else {
			err = directory.Sync()
			_ = directory.Close()
		}
	}
	if err != nil {
		return errors.New("replacement journal could not be saved")
	}
	return nil
}

func (client *arrClient) target(ctx context.Context, source string, itemID int64) (replacementTarget, error) {
	resourcePath, filePath := "movie/", "moviefile/"
	if source == "sonarr" {
		resourcePath, filePath = "episode/", "episodefile/"
	}
	var item arrResource
	if _, err := client.request(ctx, "GET", resourcePath+strconv.FormatInt(itemID, 10), nil, nil, &item); err != nil {
		return replacementTarget{}, err
	}
	fileID := item.MovieFileID
	if source == "sonarr" {
		fileID = item.EpisodeFileID
	}
	if item.ID != itemID || !item.HasFile || fileID <= 0 {
		return replacementTarget{}, errors.New("Item no longer has a replaceable file")
	}
	title := item.Title
	if source == "sonarr" {
		if item.SeriesID <= 0 {
			return replacementTarget{}, errors.New("Invalid episode series")
		}
		var episodes []arrResource
		if _, err := client.request(ctx, "GET", "episode", url.Values{"seriesId": {strconv.FormatInt(item.SeriesID, 10)}}, nil, &episodes); err != nil {
			return replacementTarget{}, err
		}
		count, matched := 0, false
		for _, episode := range episodes {
			if episode.EpisodeFileID == fileID {
				count++
				matched = matched || episode.ID == itemID
			}
		}
		if count != 1 || !matched {
			return replacementTarget{}, errors.New("Shared multi-episode files cannot be replaced here")
		}
		var series arrResource
		if _, err := client.request(ctx, "GET", "series/"+strconv.FormatInt(item.SeriesID, 10), nil, nil, &series); err != nil {
			return replacementTarget{}, err
		}
		if series.ID != item.SeriesID {
			return replacementTarget{}, errors.New("Series could not be verified")
		}
		title = fmt.Sprintf("%s - S%02d E%02d - %s", series.Title, item.Season, item.Episode, item.Title)
	} else if item.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, item.Year)
	}
	var file arrFile
	if _, err := client.request(ctx, "GET", filePath+strconv.FormatInt(fileID, 10), nil, nil, &file); err != nil {
		return replacementTarget{}, err
	}
	if file.ID != fileID || (source == "radarr" && file.MovieID != itemID) || (source == "sonarr" && file.SeriesID != item.SeriesID) {
		return replacementTarget{}, errors.New("File ownership could not be verified")
	}
	raw, _ := json.Marshal(file)
	digest := sha256.Sum256(raw)
	name := file.RelativePath
	if name == "" {
		name = file.Path
	}
	return replacementTarget{Source: source, ItemID: itemID, FileID: fileID, Title: title, Filename: path.Base(strings.ReplaceAll(name, "\\", "/")), Quality: file.Quality.Quality.Name, Size: file.Size, Fingerprint: hex.EncodeToString(digest[:])}, nil
}

func decodeLibraryWrite(writer http.ResponseWriter, request *http.Request, input any) bool {
	writer.Header().Set("Cache-Control", "no-store")
	if !sameOriginSettingsRequest(request) {
		writeJSONError(writer, 403, "Same-origin settings request required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSONError(writer, 400, "Invalid library request")
		return false
	}
	return true
}

func (app *application) replacementPreview(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
	defer cancel()
	var input struct {
		Source string `json:"source"`
		ItemID int64  `json:"item_id"`
	}
	if !decodeLibraryWrite(writer, request, &input) {
		return
	}
	service := app.arr
	client := service.clients[input.Source]
	service.mu.Lock()
	enabled := service.enabled
	service.mu.Unlock()
	if !enabled {
		writeJSONError(writer, 403, "Replacements are disabled")
		return
	}
	if client == nil || input.ItemID <= 0 || input.ItemID > 2147483647 {
		writeJSONError(writer, 400, "Invalid integration or item")
		return
	}
	target, err := client.target(ctx, input.Source, input.ItemID)
	if err != nil {
		writeJSONError(writer, 409, err.Error())
		return
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeJSONError(writer, 500, "Confirmation could not be created")
		return
	}
	preview := replacementPreview{Token: hex.EncodeToString(tokenBytes), Expires: time.Now().Add(2 * time.Minute), Target: target}
	service.mu.Lock()
	defer service.mu.Unlock()
	for token, old := range service.previews {
		if time.Now().After(old.Expires) {
			delete(service.previews, token)
		}
	}
	if len(service.previews) >= 100 {
		writeJSONError(writer, 429, "Too many pending confirmations")
		return
	}
	service.previews[preview.Token] = preview
	writeLibraryJSON(writer, 200, preview)
}

func (app *application) replacementExecute(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeLibraryWrite(writer, request, &input) {
		return
	}
	service := app.arr
	service.mu.Lock()
	defer service.mu.Unlock()
	if existing := service.jobs[input.Token]; existing != nil {
		writeLibraryJSON(writer, 200, existing)
		return
	}
	if !service.enabled {
		writeJSONError(writer, 403, "Replacements are disabled")
		return
	}
	preview, exists := service.previews[input.Token]
	if !exists || time.Now().After(preview.Expires) {
		writeJSONError(writer, 409, "Confirmation expired; preview the file again")
		return
	}
	if service.busy[preview.Target.Source] {
		writeJSONError(writer, 429, "Another replacement is being submitted; try again shortly")
		return
	}
	for _, job := range service.jobs {
		if job.Target.Source == preview.Target.Source && job.Target.ItemID == preview.Target.ItemID && job.State != "failed" && job.State != "search_finished" {
			writeJSONError(writer, 409, "An operation for this item already exists; check its status first")
			return
		}
	}
	if len(service.jobs) >= 100 {
		var oldest *replacementJob
		for _, job := range service.jobs {
			if (job.State == "failed" || job.State == "search_finished") && (oldest == nil || job.Updated.Before(oldest.Updated)) {
				oldest = job
			}
		}
		if oldest == nil {
			writeJSONError(writer, 429, "Operation journal is full; resolve pending operations first")
			return
		}
		delete(service.jobs, oldest.ID)
	}
	client := service.clients[preview.Target.Source]
	job := &replacementJob{ID: input.Token, Target: preview.Target, State: "preparing", Message: "Verifying current file", Created: time.Now(), Updated: time.Now(), Connection: client.connectionID()}
	service.jobs[job.ID] = job
	if service.saveLocked() != nil {
		delete(service.jobs, job.ID)
		service.enabled = false
		writeJSONError(writer, 503, "Journal unavailable; no deletion was attempted")
		return
	}
	delete(service.previews, input.Token)
	service.busy[job.Target.Source] = true
	writeLibraryJSON(writer, 202, job)
	go service.replace(job.ID)
}

func (service *arrService) transition(id, state, message string, commandID int64) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	job := service.jobs[id]
	job.State, job.Message, job.Updated = state, message, time.Now()
	if commandID > 0 {
		job.CommandID = commandID
	}
	if service.saveLocked() != nil {
		job.State, job.Message = "uncertain", "Journal unavailable. Check Sonarr/Radarr before retrying."
		service.enabled = false
		return false
	}
	return true
}

func (service *arrService) replace(id string) {
	service.mu.Lock()
	job := *service.jobs[id]
	service.mu.Unlock()
	defer func() { service.mu.Lock(); delete(service.busy, job.Target.Source); service.mu.Unlock() }()
	client := service.clients[job.Target.Source]
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	current, err := client.target(ctx, job.Target.Source, job.Target.ItemID)
	if err != nil || current.FileID != job.Target.FileID || current.Fingerprint != job.Target.Fingerprint {
		service.transition(id, "failed", "File changed or could not be verified. Nothing was deleted; preview again.", 0)
		return
	}
	if !service.transition(id, "deleting", "Deleting the confirmed file", 0) {
		return
	}
	endpoint := "moviefile/"
	if job.Target.Source == "sonarr" {
		endpoint = "episodefile/"
	}
	status, err := client.request(ctx, "DELETE", endpoint+strconv.FormatInt(job.Target.FileID, 10), nil, nil, nil)
	if err != nil {
		state, message := "uncertain", "File deletion could not be confirmed. Check Sonarr/Radarr; no search was submitted."
		if status >= 400 && status < 500 {
			state, message = "failed", "File deletion was rejected; no search was submitted."
		}
		service.transition(id, state, message, 0)
		return
	}
	client.mu.Lock()
	client.expires = time.Time{}
	client.mu.Unlock()
	if !service.transition(id, "deleted", "File deleted; preparing search", 0) {
		return
	}
	service.submitSearch(ctx, id, client, job.Target)
}

func (service *arrService) submitSearch(ctx context.Context, id string, client *arrClient, target replacementTarget) {
	if !service.transition(id, "searching", "File deleted; submitting automatic search", 0) {
		return
	}
	input := map[string]any{"name": "MoviesSearch", "movieIds": []int64{target.ItemID}}
	if target.Source == "sonarr" {
		input = map[string]any{"name": "EpisodeSearch", "episodeIds": []int64{target.ItemID}}
	}
	var command struct {
		ID int64 `json:"id"`
	}
	status, err := client.request(ctx, "POST", "command", nil, input, &command)
	if err != nil || command.ID <= 0 {
		state, message := "uncertain", "File deleted. Search acceptance is unknown; check the Arr queue before another search."
		if status >= 400 && status < 500 {
			state, message = "search_failed", "File deleted, but search was rejected. A search-only retry is available."
		}
		service.transition(id, state, message, 0)
		return
	}
	service.transition(id, "search_queued", "File deleted; search accepted. A download or replacement is not yet confirmed.", command.ID)
}

func (app *application) replacementJobs(writer http.ResponseWriter, request *http.Request) {
	service := app.arr
	service.mu.Lock()
	defer service.mu.Unlock()
	jobs := make([]replacementJob, 0, len(service.jobs))
	for _, job := range service.jobs {
		jobs = append(jobs, *job)
	}
	sort.Slice(jobs, func(left, right int) bool { return jobs[left].Created.After(jobs[right].Created) })
	writeLibraryJSON(writer, 200, jobs)
}

func (app *application) replacementStatus(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
	defer cancel()
	service := app.arr
	id := request.PathValue("id")
	service.mu.Lock()
	stored := service.jobs[id]
	if stored == nil {
		service.mu.Unlock()
		writeJSONError(writer, 404, "Operation not found")
		return
	}
	job := *stored
	check := job.State == "search_queued" && time.Since(stored.Checked) >= 5*time.Second
	if check {
		stored.Checked = time.Now()
	}
	service.mu.Unlock()
	client := service.clients[job.Target.Source]
	if check && client != nil && client.connectionID() == job.Connection {
		var command struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		}
		if _, err := client.request(ctx, "GET", "command/"+strconv.FormatInt(job.CommandID, 10), nil, nil, &command); err == nil && command.ID == job.CommandID {
			if command.Status == "completed" {
				service.transition(id, "search_finished", "Search finished. Check Sonarr/Radarr for download and import results; a new file is not guaranteed.", 0)
			}
			if command.Status == "failed" || command.Status == "aborted" || command.Status == "cancelled" {
				service.transition(id, "uncertain", "Search did not finish successfully. Check the Arr queue before retrying; a download may have started.", 0)
			}
		}
	}
	service.mu.Lock()
	job = *service.jobs[id]
	service.mu.Unlock()
	writeLibraryJSON(writer, 200, job)
}

func (app *application) replacementRetry(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if !decodeLibraryWrite(writer, request, &input) {
		return
	}
	service := app.arr
	service.mu.Lock()
	job := service.jobs[input.ID]
	if !service.enabled || job == nil || job.State != "search_failed" || service.busy[job.Target.Source] {
		service.mu.Unlock()
		writeJSONError(writer, 409, "Search-only retry is unavailable")
		return
	}
	client := service.clients[job.Target.Source]
	if client == nil || client.connectionID() != job.Connection {
		service.mu.Unlock()
		writeJSONError(writer, 409, "Integration changed; check Arr directly")
		return
	}
	target := job.Target
	service.busy[target.Source] = true
	job.State, job.Message, job.Updated = "preparing", "Verifying item before search-only retry", time.Now()
	if service.saveLocked() != nil {
		job.State, job.Message = "uncertain", "Journal unavailable; no retry was submitted"
		service.enabled = false
		delete(service.busy, target.Source)
		service.mu.Unlock()
		writeJSONError(writer, 503, "Journal unavailable; no retry was submitted")
		return
	}
	result := *job
	service.mu.Unlock()
	writeLibraryJSON(writer, 202, result)
	go func() {
		defer func() { service.mu.Lock(); delete(service.busy, target.Source); service.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		endpoint := "movie/"
		if target.Source == "sonarr" {
			endpoint = "episode/"
		}
		var item arrResource
		if _, err := client.request(ctx, "GET", endpoint+strconv.FormatInt(target.ItemID, 10), nil, nil, &item); err != nil || item.ID != target.ItemID || item.HasFile || item.MovieFileID > 0 || item.EpisodeFileID > 0 {
			service.transition(input.ID, "search_failed", "No retry submitted: a file is present or the item could not be verified. Check Arr directly.", 0)
			return
		}
		service.submitSearch(ctx, input.ID, client, target)
	}()
}
