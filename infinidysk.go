package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/common/expfmt"
)

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

type ifdMonitor struct {
	mu              sync.RWMutex
	url             *url.URL
	key             string
	client          *http.Client
	data            ifdSnapshot
	previousFetched float64
	previousServed  float64
	previousAt      time.Time
	readsAt         time.Time
	liveAt          time.Time
	connected       bool
}

func loadIFDConfig() (*url.URL, string, error) {
	raw, key := strings.TrimSpace(os.Getenv("IFD_URL")), strings.TrimSpace(os.Getenv("IFD_API_KEY"))
	if raw == "" && key == "" {
		return nil, "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || key == "" {
		return nil, "", errors.New("IFD_URL must be an absolute HTTP(S) backend URL without credentials, query or fragment, and IFD_API_KEY is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, key, nil
}

func (monitor *ifdMonitor) get(ctx context.Context, suffix string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, monitor.url.String()+suffix, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("x-api-key", monitor.key)
	response, err := monitor.client.Do(request)
	if err != nil {
		return nil, errors.New("IFD request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("IFD endpoint unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if len(body) > maxResponseBytes {
		return nil, errors.New("IFD response too large")
	}
	return body, err
}

func ifdMetrics(body []byte) (float64, *float64, error) {
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, err
	}
	served := families["nzbdav_bytes_served_total"]
	if served == nil || len(served.Metric) != 1 || served.Metric[0].Counter == nil {
		return 0, nil, errors.New("IFD byte counter unavailable")
	}
	var connections *float64
	for _, metric := range families["nzbdav_nntp_pool_connections"].GetMetric() {
		for _, label := range metric.Label {
			if label.GetName() == "state" && label.GetValue() == "active" && metric.Gauge != nil {
				if connections == nil {
					connections = new(float64)
				}
				*connections += metric.Gauge.GetValue()
			}
		}
	}
	bytes := served.Metric[0].Counter.GetValue()
	if !ifdNumber(bytes) || (connections != nil && !ifdNumber(*connections)) {
		return 0, nil, errors.New("Invalid IFD metrics")
	}
	return bytes, connections, nil
}

func ifdNumber(value float64) bool { return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }

func ifdRate(current, previous float64, elapsed time.Duration) *float64 {
	if elapsed <= 0 || elapsed > 15*time.Second || !ifdNumber(current) || !ifdNumber(previous) || current < previous {
		return nil
	}
	rate := (current - previous) / elapsed.Seconds()
	return &rate
}

func (monitor *ifdMonitor) sample(ctx context.Context) {
	metricsBody, metricsErr := monitor.get(ctx, "/metrics")
	statsBody, statsErr := monitor.get(ctx, "/api?mode=server_stats&output=json")
	var stats struct {
		Total *float64 `json:"total"`
	}
	served, connections, parseErr := ifdMetrics(metricsBody)
	statsParseErr := json.Unmarshal(statsBody, &stats)
	now := time.Now()
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	point := ifdSample{Timestamp: now.UnixMilli()}
	monitor.data.Available = metricsErr == nil && statsErr == nil && parseErr == nil && statsParseErr == nil && stats.Total != nil && ifdNumber(*stats.Total)
	if monitor.data.Available {
		point.Fetched = ifdRate(*stats.Total, monitor.previousFetched, now.Sub(monitor.previousAt))
		point.Served = ifdRate(served, monitor.previousServed, now.Sub(monitor.previousAt))
		monitor.previousFetched, monitor.previousServed, monitor.previousAt = *stats.Total, served, now
		monitor.data.Connections, monitor.data.SampledAt = connections, now.UnixMilli()
	} else {
		monitor.previousAt = time.Time{}
		monitor.data.Connections = nil
	}
	monitor.data.History = append(monitor.data.History, point)
	for len(monitor.data.History) > 0 && monitor.data.History[0].Timestamp < now.Add(-historyWindow).UnixMilli() {
		monitor.data.History = monitor.data.History[1:]
	}
}

func (monitor *ifdMonitor) run(ctx context.Context) {
	go monitor.stream(ctx)
	monitor.sample(ctx)
	ticker := time.NewTicker(resourceInterval)
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

func (monitor *ifdMonitor) message(topic, message string, now time.Time) error {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	switch topic {
	case "ls":
		var live struct {
			ActiveReads *int  `json:"activeReads"`
			Timestamp   int64 `json:"ts"`
		}
		if err := json.Unmarshal([]byte(message), &live); err != nil {
			return err
		}
		if live.ActiveReads == nil || *live.ActiveReads < 0 || live.Timestamp < now.Add(-30*time.Second).UnixMilli() {
			return errors.New("Invalid IFD heartbeat")
		}
		monitor.liveAt = now
		if *live.ActiveReads == 0 {
			monitor.data.Files = []ifdRead{}
			monitor.readsAt = now
		}
	case "ar":
		var payload struct {
			Reads []ifdRead `json:"reads"`
		}
		if err := json.Unmarshal([]byte(message), &payload); err != nil {
			return err
		}
		if payload.Reads == nil || len(payload.Reads) > 1000 {
			return errors.New("Invalid IFD reads")
		}
		previous := make(map[string]ifdRead, len(monitor.data.Files))
		for _, read := range monitor.data.Files {
			previous[read.ID] = read
		}
		for index := range payload.Reads {
			read := &payload.Reads[index]
			read.Rate = nil
			if read.ID == "" || (read.Fetched != nil && !ifdNumber(*read.Fetched)) {
				return errors.New("Invalid IFD read")
			}
			if old, exists := previous[read.ID]; exists && read.Fetched != nil && old.Fetched != nil {
				read.Rate = ifdRate(*read.Fetched, *old.Fetched, now.Sub(monitor.readsAt))
			}
		}
		monitor.data.Files, monitor.readsAt = payload.Reads, now
	}
	return nil
}

func (monitor *ifdMonitor) stream(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		monitor.streamOnce(ctx)
		monitor.mu.Lock()
		monitor.connected = false
		monitor.readsAt, monitor.liveAt = time.Time{}, time.Time{}
		monitor.data.Files = nil
		monitor.mu.Unlock()
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (monitor *ifdMonitor) streamOnce(ctx context.Context) {
	endpoint := *monitor.url
	endpoint.Path += "/ws"
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Proxy: http.ProxyFromEnvironment}
	connection, response, err := dialer.DialContext(ctx, endpoint.String(), nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	connection.SetReadLimit(maxResponseBytes)
	connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if connection.WriteMessage(websocket.TextMessage, []byte(monitor.key)) != nil {
		return
	}
	if connection.WriteJSON(map[string][]string{"sub": {"ar", "ls"}}) != nil {
		return
	}
	monitor.mu.Lock()
	monitor.connected = true
	monitor.mu.Unlock()
	for {
		connection.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, raw, err := connection.ReadMessage()
		if err != nil {
			return
		}
		var envelope struct {
			Topic   string
			Message string
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return
		}
		if monitor.message(envelope.Topic, envelope.Message, time.Now()) != nil {
			return
		}
	}
}

func (a *application) ifdJSON(w http.ResponseWriter, request *http.Request) {
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
	snapshot.FilesAvailable = a.ifd.connected && !a.ifd.readsAt.IsZero() && time.Since(a.ifd.liveAt) < 15*time.Second
	snapshot.Files = append([]ifdRead{}, snapshot.Files...)
	if !snapshot.FilesAvailable {
		snapshot.Files = nil
	} else if time.Since(a.ifd.readsAt) > 3*time.Second {
		for index := range snapshot.Files {
			snapshot.Files[index].Rate = new(float64)
		}
	}
	json.NewEncoder(w).Encode(snapshot)
}
