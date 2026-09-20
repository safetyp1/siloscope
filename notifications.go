package main

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type notificationRules struct {
	ServiceDown   bool `json:"service_down"`
	Disk          bool `json:"disk"`
	DiskThreshold int  `json:"disk_threshold"`
	CPU           bool `json:"cpu"`
	Threshold     int  `json:"threshold"`
	Minutes       int  `json:"minutes"`
	Cooldown      int  `json:"cooldown"`
	Recovery      bool `json:"recovery"`
	Playback      bool `json:"playback"`
	Transcode     bool `json:"transcode"`
	Details       bool `json:"details"`
}

func defaultNotificationRules() notificationRules {
	return notificationRules{CPU: true, Threshold: 85, Minutes: 5, Cooldown: 30, Recovery: true, DiskThreshold: 90}
}

func (rules notificationRules) valid() bool {
	return rules.Threshold >= 1 && rules.Threshold <= 100 && rules.Minutes >= 1 && rules.Minutes <= 60 && rules.Cooldown >= 1 && rules.Cooldown <= 1440 && ((rules.DiskThreshold >= 1 && rules.DiskThreshold <= 100) || (!rules.Disk && rules.DiskThreshold == 0))
}

type notificationDevice struct {
	PendingTranscodes map[string]map[string]*transcodeCheck `json:"-"`
	Outages           map[string]*serviceOutage             `json:"outages,omitempty"`
	DiskActive        map[string]bool                       `json:"disk_active,omitempty"`
	Subscription      webpush.Subscription                  `json:"subscription"`
	Rules             notificationRules                     `json:"rules"`
	HighSince         time.Time                             `json:"high_since"`
	LastCPU           time.Time                             `json:"last_cpu"`
	CPUActive         bool                                  `json:"cpu_active"`
	Seen              map[string]map[string]bool            `json:"seen"`
	SeenAt            map[string]time.Time                  `json:"seen_at"`
	LastTest          time.Time                             `json:"last_test"`
}

type transcodeCheck struct {
	Started time.Time
	Checked bool
	Notify  bool
}

type notificationStore struct {
	PrivateKey string                         `json:"private_key"`
	PublicKey  string                         `json:"public_key"`
	Devices    map[string]*notificationDevice `json:"devices"`
}

type pushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url"`
}

type pushDelivery struct {
	Device  string
	Message pushMessage
}

type notificationService struct {
	mu         sync.Mutex
	store      notificationStore
	path       string
	contact    string
	queue      chan pushDelivery
	lastSample time.Time
	client     *http.Client
}

func newNotificationService(directory, contact string) (*notificationService, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	service := &notificationService{
		path: filepath.Join(directory, "notifications.json"), contact: contact,
		queue:  make(chan pushDelivery, 128),
		client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	raw, err := os.ReadFile(service.path)
	if err == nil {
		if json.Unmarshal(raw, &service.store) != nil || service.store.PrivateKey == "" || service.store.PublicKey == "" || service.store.Devices == nil {
			return nil, errors.New("invalid notification store")
		}
		for _, device := range service.store.Devices {
			if device == nil || !validPushSubscription(device.Subscription) || !device.Rules.valid() {
				return nil, errors.New("invalid notification device")
			}
			device.HighSince = time.Time{}
			if device.Rules.DiskThreshold == 0 {
				device.Rules.DiskThreshold = 90
			}
			device.SeenAt = make(map[string]time.Time)
		}
		return service, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, err
	}
	service.store = notificationStore{PrivateKey: privateKey, PublicKey: publicKey, Devices: make(map[string]*notificationDevice)}
	if err := service.save(); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *notificationService) save() error {
	raw, err := json.Marshal(service.store)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(service.path), ".notifications-*")
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
	return os.Rename(file.Name(), service.path)
}

func validPushSubscription(subscription webpush.Subscription) bool {
	endpoint, err := url.Parse(subscription.Endpoint)
	if err != nil || len(subscription.Endpoint) > 4096 || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Fragment != "" || endpoint.Port() != "" {
		return false
	}
	host := endpoint.Hostname()
	if host != "web.push.apple.com" && !strings.HasSuffix(host, ".push.apple.com") && host != "fcm.googleapis.com" && host != "updates.push.services.mozilla.com" && !strings.HasSuffix(host, ".push.services.mozilla.com") {
		return false
	}
	auth, err := base64.RawURLEncoding.DecodeString(subscription.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return false
	}
	key, err := base64.RawURLEncoding.DecodeString(subscription.Keys.P256dh)
	if err != nil {
		return false
	}
	_, err = ecdh.P256().NewPublicKey(key)
	return err == nil
}

func notificationDeviceID(subscription webpush.Subscription) string {
	digest := sha256.Sum256([]byte(subscription.Endpoint))
	return hex.EncodeToString(digest[:])
}

func (app *application) notificationsJSON(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	service := app.notifications
	if request.Method == http.MethodGet {
		publicKey := ""
		if service != nil {
			service.mu.Lock()
			publicKey = service.store.PublicKey
			service.mu.Unlock()
		}
		_ = json.NewEncoder(writer).Encode(struct {
			Enabled   bool              `json:"enabled"`
			PublicKey string            `json:"public_key"`
			Defaults  notificationRules `json:"defaults"`
		}{service != nil, publicKey, defaultNotificationRules()})
		return
	}
	if !sameOriginSettingsRequest(request) {
		writeJSONError(writer, http.StatusForbidden, "Same-origin request required")
		return
	}
	if service == nil {
		writeJSONError(writer, http.StatusServiceUnavailable, "Push notifications are not configured on this server")
		return
	}
	var input struct {
		Action       string               `json:"action"`
		Subscription webpush.Subscription `json:"subscription"`
		Rules        *notificationRules   `json:"rules"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 12<<10))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF || !validPushSubscription(input.Subscription) {
		writeJSONError(writer, http.StatusBadRequest, "Invalid push subscription")
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	id := notificationDeviceID(input.Subscription)
	device := service.store.Devices[id]
	if device != nil && device.Subscription.Keys != input.Subscription.Keys {
		writeJSONError(writer, http.StatusForbidden, "Subscription keys do not match")
		return
	}
	before, _ := json.Marshal(service.store)
	switch input.Action {
	case "get":
		if device == nil {
			writeJSONError(writer, http.StatusNotFound, "Device is not registered")
			return
		}
	case "save":
		if input.Rules == nil || !input.Rules.valid() {
			writeJSONError(writer, http.StatusBadRequest, "Invalid notification rules")
			return
		}
		if input.Rules.DiskThreshold == 0 {
			input.Rules.DiskThreshold = 90
		}
		if device == nil {
			if len(service.store.Devices) >= 32 {
				writeJSONError(writer, http.StatusConflict, "Notification device limit reached")
				return
			}
			device = &notificationDevice{Subscription: input.Subscription}
			service.store.Devices[id] = device
		}
		if device.Rules.Disk != input.Rules.Disk || device.Rules.DiskThreshold != input.Rules.DiskThreshold {
			device.DiskActive = nil
		}
		if device.Rules.ServiceDown != input.Rules.ServiceDown {
			device.Outages = nil
		}
		if device.Rules != *input.Rules {
			device.HighSince = time.Time{}
			device.CPUActive = false
			device.Seen = nil
			device.SeenAt = nil
			device.PendingTranscodes = nil
		}
		device.Rules = *input.Rules
	case "delete":
		delete(service.store.Devices, id)
	case "test":
		if device == nil {
			writeJSONError(writer, http.StatusNotFound, "Device is not registered")
			return
		}
		if time.Since(device.LastTest) < time.Minute {
			writeJSONError(writer, http.StatusTooManyRequests, "Wait one minute before another test")
			return
		}
		device.LastTest = time.Now()
	default:
		writeJSONError(writer, http.StatusBadRequest, "Unknown notification action")
		return
	}
	if input.Action != "get" {
		if err := service.save(); err != nil {
			var restored notificationStore
			_ = json.Unmarshal(before, &restored)
			service.store = restored
			writeJSONError(writer, http.StatusInternalServerError, "Notification settings could not be saved")
			return
		}
	}
	if input.Action == "test" {
		if !service.enqueue(id, pushMessage{Title: "Siloscope", Body: "Notifications are enabled on this device.", Tag: "test", URL: "/"}) {
			writeJSONError(writer, http.StatusServiceUnavailable, "Notification queue is busy")
			return
		}
	}
	rules := defaultNotificationRules()
	if device != nil {
		rules = device.Rules
	}
	_ = json.NewEncoder(writer).Encode(struct {
		Rules notificationRules `json:"rules"`
	}{rules})
}

func (service *notificationService) enqueue(id string, message pushMessage) bool {
	select {
	case service.queue <- pushDelivery{Device: id, Message: message}:
		return true
	default:
		return false
	}
}
