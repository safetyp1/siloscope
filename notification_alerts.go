package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type notificationSession struct {
	Poster      string          `json:"poster_url"`
	Season      *int            `json:"season_number"`
	Episode     *int            `json:"episode_number"`
	EpisodeName string          `json:"episode_name"`
	ID          json.RawMessage `json:"id"`
	SessionID   string          `json:"session_id"`
	Title       string          `json:"media_title"`
	MediaType   string          `json:"media_type"`
	Series      string          `json:"series_name"`
	Username    string          `json:"username"`
	Profile     string          `json:"profile_name"`
	Method      string          `json:"play_method"`
	Effective   string          `json:"effective_play_method"`
	Video       string          `json:"video_decision"`
	Audio       string          `json:"audio_decision"`
}

func (session notificationSession) key() string {
	if session.SessionID != "" {
		return session.SessionID
	}
	if len(session.ID) == 0 || string(session.ID) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(session.ID, &text) == nil {
		return text
	}
	return string(session.ID)
}

func (session notificationSession) transcoding() bool {
	for _, value := range []string{session.Method, session.Effective, session.Video, session.Audio} {
		value = strings.ToLower(value)
		if strings.Contains(value, "transcode") || value == "audio" {
			return true
		}
	}
	return false
}

func (session notificationSession) confirmedTranscoding() bool {
	for _, value := range []string{session.Video, session.Audio} {
		if strings.EqualFold(value, "transcode") {
			return true
		}
	}
	method := session.Effective
	if method == "" {
		method = session.Method
	}
	method = strings.ToLower(method)
	return strings.Contains(method, "transcode") || method == "audio"
}

func (session notificationSession) notificationDetail(source string) string {
	label := "Media"
	title := strings.TrimSpace(session.Title)
	switch strings.ToLower(strings.TrimSpace(session.MediaType)) {
	case "episode", "tv", "show", "series":
		label = "TV"
		if series := strings.TrimSpace(session.Series); series != "" {
			title = series
		}
	case "movie":
		label = "Movie"
	case "track", "music", "audio":
		label = "Music"
	}
	if title == "" {
		title = "Unknown title"
	}
	body := label + ": " + title
	name := strings.TrimSpace(session.Username)
	if name == "" && source == "Silo" {
		name = strings.TrimSpace(session.Profile)
	}
	if name != "" {
		body += " (" + name + ")"
	}
	if text := []rune(body); len(text) > 180 {
		body = string(text[:180])
	}
	return body
}

type notificationDisk struct {
	Stale       bool     `json:"stale"`
	Path        string   `json:"path"`
	Role        string   `json:"role"`
	Scratch     bool     `json:"scratch"`
	Unavailable bool     `json:"unavailable"`
	Used        *float64 `json:"used_gb"`
	Total       *float64 `json:"total_gb"`
}

func (service *notificationService) observeDisks(disks []notificationDisk, sampledAt, now time.Time) {
	if sampledAt.IsZero() || now.Sub(sampledAt) < 0 || now.Sub(sampledAt) > 15*time.Second {
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	changed := false
	for _, disk := range disks {
		if disk.Stale || disk.Unavailable || disk.Used == nil || disk.Total == nil || *disk.Total <= 0 || *disk.Used < 0 || *disk.Used > *disk.Total || math.IsNaN(*disk.Used) || math.IsNaN(*disk.Total) || math.IsInf(*disk.Used, 0) || math.IsInf(*disk.Total, 0) {
			continue
		}
		key := disk.Path
		if key == "" {
			key = fmt.Sprintf("%s:%t", disk.Role, disk.Scratch)
		}
		label := "Media disk"
		if disk.Scratch {
			label = "System disk"
		} else if disk.Role != "" {
			label = disk.Role
		}
		percent := *disk.Used / *disk.Total * 100
		for id, device := range service.store.Devices {
			if !device.Rules.Disk {
				continue
			}
			if percent >= float64(device.Rules.DiskThreshold) && !device.DiskActive[key] {
				message := pushMessage{Title: "High disk usage", Body: fmt.Sprintf("%s usage exceeded %d%%", label, device.Rules.DiskThreshold), Tag: fmt.Sprintf("disk-high-%x", sha256.Sum256([]byte(key))), URL: "/?view=system"}
				if service.enqueue(id, message) {
					if device.DiskActive == nil {
						device.DiskActive = make(map[string]bool)
					}
					device.DiskActive[key] = true
					changed = true
				}
			} else if device.DiskActive[key] && percent <= float64(max(0, device.Rules.DiskThreshold-5)) {
				delete(device.DiskActive, key)
				changed = true
			}
		}
	}
	if changed {
		service.persistAlerts()
	}
}

func (service *notificationService) observeCPU(sample resourceHistorySample, now time.Time) {
	service.mu.Lock()
	defer service.mu.Unlock()
	sampledAt := time.UnixMilli(sample.Timestamp)
	fresh := sample.CPU != nil && !math.IsNaN(*sample.CPU) && !math.IsInf(*sample.CPU, 0) && *sample.CPU >= 0 && *sample.CPU <= 100 && now.Sub(sampledAt) >= 0 && now.Sub(sampledAt) <= 15*time.Second
	if fresh && sampledAt.Equal(service.lastSample) {
		return
	}
	gap := !fresh || (!service.lastSample.IsZero() && sampledAt.Sub(service.lastSample) > 15*time.Second)
	if fresh {
		service.lastSample = sampledAt
	}
	changed := false
	for id, device := range service.store.Devices {
		if !device.Rules.CPU {
			continue
		}
		if gap && !device.HighSince.IsZero() {
			device.HighSince = time.Time{}
			changed = true
		}
		if !fresh {
			continue
		}
		if *sample.CPU >= float64(device.Rules.Threshold) {
			if device.HighSince.IsZero() {
				device.HighSince = sampledAt
				changed = true
			}
			if sampledAt.Sub(device.HighSince) >= time.Duration(device.Rules.Minutes)*time.Minute && now.Sub(device.LastCPU) >= time.Duration(device.Rules.Cooldown)*time.Minute {
				message := pushMessage{Title: "Sustained high CPU", Body: fmt.Sprintf("CPU is %.0f%%, above %d%% for at least %d minutes.", *sample.CPU, device.Rules.Threshold, device.Rules.Minutes), Tag: "cpu-high", URL: "/?view=system"}
				if service.enqueue(id, message) {
					device.LastCPU = now
					device.CPUActive = true
					changed = true
				}
			}
		} else {
			if !device.HighSince.IsZero() {
				device.HighSince = time.Time{}
				changed = true
			}
			if device.CPUActive && *sample.CPU <= float64(max(0, device.Rules.Threshold-5)) {
				if !device.Rules.Recovery || service.enqueue(id, pushMessage{Title: "CPU recovered", Body: fmt.Sprintf("CPU usage is now %.0f%%.", *sample.CPU), Tag: "cpu-recovery", URL: "/?view=system"}) {
					device.CPUActive = false
					changed = true
				}
			}
		}
	}
	if changed {
		service.persistAlerts()
	}
}

func (service *notificationService) observeSessions(source string, sessions []notificationSession, now time.Time) {
	service.mu.Lock()
	defer service.mu.Unlock()
	current := make(map[string]bool)
	for _, session := range sessions {
		if key := session.key(); key != "" {
			current[key] = session.transcoding()
		}
	}
	changed := false
	for id, device := range service.store.Devices {
		if !device.Rules.Playback && !device.Rules.Transcode {
			delete(device.PendingTranscodes, source)
			continue
		}
		if device.Seen == nil {
			device.Seen = make(map[string]map[string]bool)
		}
		if device.SeenAt == nil {
			device.SeenAt = make(map[string]time.Time)
		}
		previous, initialized := device.Seen[source]
		continuous := initialized && !device.SeenAt[source].IsZero() && now.Sub(device.SeenAt[source]) >= 0 && now.Sub(device.SeenAt[source]) <= 30*time.Second
		if device.PendingTranscodes == nil {
			device.PendingTranscodes = make(map[string]map[string]*transcodeCheck)
		}
		if !continuous || !device.Rules.Transcode || device.PendingTranscodes[source] == nil {
			device.PendingTranscodes[source] = make(map[string]*transcodeCheck)
		}
		pending := device.PendingTranscodes[source]
		for key := range pending {
			if _, exists := current[key]; !exists {
				delete(pending, key)
			}
		}
		if continuous {
			var started, transcoded []notificationSession
			for _, session := range sessions {
				key := session.key()
				if key == "" {
					continue
				}
				_, exists := previous[key]
				if !exists && device.Rules.Playback {
					started = append(started, session)
				}
				if !exists && device.Rules.Transcode {
					pending[key] = &transcodeCheck{Started: now}
				}
				if check := pending[key]; check != nil && now.Sub(check.Started) >= time.Minute {
					if !check.Checked {
						check.Checked = true
						check.Notify = session.confirmedTranscoding()
					}
					if check.Notify && now.Sub(check.Started) <= 90*time.Second {
						transcoded = append(transcoded, session)
					}
				}
			}
			for kind, matches := range map[string][]notificationSession{"Playback": started, "Transcode": transcoded} {
				if len(matches) == 0 {
					continue
				}
				body := fmt.Sprintf("%s: %d new %s event(s).", source, len(matches), strings.ToLower(kind))
				if device.Rules.Details && len(matches) == 1 {
					body = matches[0].notificationDetail(source)
				}
				if service.enqueue(id, pushMessage{Title: kind + " started", Body: body, Tag: source + "-" + strings.ToLower(kind), URL: "/?view=playing"}) && kind == "Transcode" {
					for _, session := range matches {
						pending[session.key()].Notify = false
					}
				}
			}
		}
		if !initialized || !maps.Equal(previous, current) {
			changed = true
		}
		device.Seen[source] = current
		device.SeenAt[source] = now
	}
	if changed {
		service.persistAlerts()
	}
}

func (service *notificationService) persistAlerts() {
	if err := service.save(); err != nil {
		log.Print("notification alert state could not be saved")
	}
}

func (app *application) runNotifications(ctx context.Context) {
	service := app.notifications
	go service.deliver(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			app.history.mu.RLock()
			var sample resourceHistorySample
			if len(app.history.samples) > 0 {
				sample = app.history.samples[len(app.history.samples)-1]
			}
			app.history.mu.RUnlock()
			service.observeCPU(sample, now)
		}
	}
}

func (service *notificationService) deliver(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case delivery := <-service.queue:
			service.mu.Lock()
			device := service.store.Devices[delivery.Device]
			if device == nil {
				service.mu.Unlock()
				continue
			}
			subscription := device.Subscription
			privateKey, publicKey := service.store.PrivateKey, service.store.PublicKey
			service.mu.Unlock()
			body, _ := json.Marshal(delivery.Message)
			response, err := webpush.SendNotificationWithContext(ctx, body, &subscription, &webpush.Options{
				Subscriber: strings.TrimPrefix(service.contact, "mailto:"), VAPIDPrivateKey: privateKey, VAPIDPublicKey: publicKey,
				TTL: 300, HTTPClient: service.client, Urgency: webpush.UrgencyNormal,
			})
			if err != nil {
				log.Print("push delivery failed; waiting for the next event")
				continue
			}
			response.Body.Close()
			if response.StatusCode == http.StatusGone || response.StatusCode == http.StatusNotFound {
				service.mu.Lock()
				delete(service.store.Devices, delivery.Device)
				service.persistAlerts()
				service.mu.Unlock()
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				log.Printf("push service returned HTTP %d", response.StatusCode)
			} else if delivery.Message.Tag == "test" {
				log.Printf("push test notification accepted by service (HTTP %d)", response.StatusCode)
			}
		}
	}
}
