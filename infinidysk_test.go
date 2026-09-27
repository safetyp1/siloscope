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

	"github.com/gorilla/websocket"
)

const ifdMetricsFixture = `# TYPE nzbdav_bytes_served_total counter
nzbdav_bytes_served_total 1000
# TYPE nzbdav_nntp_pool_connections gauge
nzbdav_nntp_pool_connections{provider_key="first",state="active"} 4
nzbdav_nntp_pool_connections{provider_key="first",state="idle"} 6
nzbdav_nntp_pool_connections{provider_key="backup",state="active"} 2
nzbdav_nntp_pool_connections{provider_key="first",state="transfer_active"} 4
`

func TestIFDMetrics(t *testing.T) {
	served, connections, err := ifdMetrics([]byte(ifdMetricsFixture))
	if err != nil || served != 1000 || connections == nil || *connections != 6 {
		t.Fatalf("unexpected metrics: %v %v %v", served, connections, err)
	}
	if _, _, err := ifdMetrics([]byte("<html>login</html>")); err == nil {
		t.Fatal("accepted login page")
	}
	if _, _, err := ifdMetrics([]byte(strings.ReplaceAll(ifdMetricsFixture, "1000", "NaN"))); err == nil {
		t.Fatal("accepted NaN")
	}
}

func TestIFDRatesAndReads(t *testing.T) {
	for _, elapsed := range []time.Duration{0, -time.Second, 16 * time.Second} {
		if ifdRate(100, 50, elapsed) != nil {
			t.Fatal("accepted invalid interval")
		}
	}
	if ifdRate(20, 100, time.Second) != nil {
		t.Fatal("counter reset became a rate")
	}
	monitor := &ifdMonitor{}
	now := time.Now()
	if err := monitor.message("ar", `{"reads":[{"id":"one","fileName":"test.mkv","bytesFetched":100}]}`, now); err != nil {
		t.Fatal(err)
	}
	if monitor.data.Files[0].Rate != nil {
		t.Fatal("first sample must not show zero")
	}
	monitor.message("ar", `{"reads":[{"id":"one","fileName":"test.mkv","bytesFetched":500,"providers":[{"host":"private"}]}]}`, now.Add(2*time.Second))
	if *monitor.data.Files[0].Rate != 200 {
		t.Fatal("incorrect file rate")
	}
	body, _ := json.Marshal(monitor.data)
	if strings.Contains(string(body), "private") || strings.Contains(string(body), "providers") {
		t.Fatal("provider information leaked")
	}
	if err := monitor.message("ar", `{"reads":[{"id":"bad","bytesFetched":-1}]}`, now); err == nil {
		t.Fatal("accepted negative counter")
	}
	monitor.message("ls", fmt.Sprintf(`{"activeReads":0,"ts":%d}`, now.UnixMilli()), now)
	if len(monitor.data.Files) != 0 {
		t.Fatal("idle heartbeat did not clear files")
	}
}

func TestIFDSamplingAndFailure(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-api-key") != "test-key" {
			t.Error("missing API key")
		}
		if fail {
			http.Error(w, "failed", 503)
			return
		}
		if request.URL.Path == "/metrics" {
			fmt.Fprint(w, ifdMetricsFixture)
		} else {
			fmt.Fprint(w, `{"total":2000}`)
		}
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	monitor := &ifdMonitor{url: endpoint, key: "test-key", client: server.Client()}
	monitor.sample(context.Background())
	if !monitor.data.Available || monitor.data.History[0].Fetched != nil {
		t.Fatal("first sample should be available without a rate")
	}
	monitor.previousAt = time.Now().Add(-5 * time.Second)
	monitor.previousFetched = 1000
	monitor.sample(context.Background())
	if rate := monitor.data.History[1].Fetched; rate == nil || *rate < 190 || *rate > 210 {
		t.Fatalf("bad rate: %v", rate)
	}
	fail = true
	monitor.sample(context.Background())
	if monitor.data.Available || monitor.data.History[2].Fetched != nil || !monitor.previousAt.IsZero() {
		t.Fatal("failed sample must leave a gap and reset baseline")
	}
}

func TestIFDConfig(t *testing.T) {
	t.Setenv("IFD_URL", "")
	t.Setenv("IFD_API_KEY", "")
	if endpoint, _, err := loadIFDConfig(); endpoint != nil || err != nil {
		t.Fatal("optional config failed")
	}
	t.Setenv("IFD_API_KEY", "key")
	for _, raw := range []string{"", "ftp://host", "http://user:pass@host", "http://host?key=secret", "http://host/#fragment"} {
		t.Setenv("IFD_URL", raw)
		if _, _, err := loadIFDConfig(); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	t.Setenv("IFD_URL", "http://nzbdav:8080/")
	if endpoint, _, err := loadIFDConfig(); err != nil || endpoint.String() != "http://nzbdav:8080" {
		t.Fatal("valid config failed")
	}
}

func TestIFDWebsocketAndSnapshot(t *testing.T) {
	upgrader := websocket.Upgrader{}
	received := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, key, _ := connection.ReadMessage()
		if string(key) != "test-key" {
			t.Error("missing websocket authentication")
		}
		var subscription struct {
			Sub []string `json:"sub"`
		}
		if err := connection.ReadJSON(&subscription); err != nil || len(subscription.Sub) != 2 {
			t.Error("missing subscription")
		}
		connection.WriteJSON(map[string]string{"Topic": "ar", "Message": `{"reads":[{"id":"one","fileName":"example.mkv","bytesFetched":50}]}`})
		connection.WriteJSON(map[string]string{"Topic": "ls", "Message": fmt.Sprintf(`{"activeReads":1,"ts":%d}`, time.Now().UnixMilli())})
		close(received)
		connection.ReadMessage()
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	monitor := &ifdMonitor{url: endpoint, key: "test-key"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { monitor.streamOnce(ctx); close(done) }()
	<-received
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("no live snapshot")
		case <-ticker.C:
			response := httptest.NewRecorder()
			(&application{ifd: monitor}).ifdJSON(response, httptest.NewRequest("GET", "/api/ifd", nil))
			var snapshot ifdSnapshot
			json.Unmarshal(response.Body.Bytes(), &snapshot)
			if !snapshot.FilesAvailable {
				continue
			}
			if len(snapshot.Files) != 1 {
				t.Fatal("missing file")
			}
			cancel()
			select {
			case <-done:
				return
			case <-time.After(time.Second):
				t.Fatal("socket did not close on shutdown")
			}
		}
	}
}
