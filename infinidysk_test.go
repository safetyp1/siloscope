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

func ifdLiveFixture(at time.Time) string {
	return fmt.Sprintf(`{
		"at":%d,"fetch_bps":3000,"read_bps":2000,
		"providers":[{"name":"private-provider","active":4,"live":10,"busy":6},{"active":2}],
		"reads":[{"id":42,"path":"/library/Movies/example.mkv","client":"private-client",
			"served":9000,"bps":8000,"sources":[{"name":"private-provider","bytes":1000,"bps":300},{"bytes":2000,"bps":500}]},
			{"id":43,"path":"/library/cached.mkv","served":7000,"bps":2000,"sources":[]}]
	}`, at.UnixMilli())
}

func TestIFDV2LiveData(t *testing.T) {
	now := time.Now()
	var live ifdLive
	if err := json.Unmarshal([]byte(ifdLiveFixture(now)), &live); err != nil {
		t.Fatal(err)
	}
	files, connections, err := live.files(now)
	if err != nil || connections == nil || *connections != 6 || len(files) != 2 {
		t.Fatalf("incorrect live data: %+v %v %v", files, connections, err)
	}
	if files[0].ID != "42" || files[0].Name != "example.mkv" || *files[0].Fetched != 3000 || *files[0].Rate != 800 {
		t.Fatalf("provider fetches were not summed correctly: %+v", files[0])
	}
	if *files[1].Fetched != 0 || *files[1].Rate != 0 {
		t.Fatal("cached reads must not count served bytes as fetched bytes")
	}
	body, err := json.Marshal(files)
	if err != nil || strings.Contains(string(body), "private") || strings.Contains(string(body), "/library/") {
		t.Fatalf("unnecessary upstream details exposed: %s %v", body, err)
	}
}

func TestIFDV2InvalidLiveData(t *testing.T) {
	now := time.Now()
	fixture := ifdLiveFixture(now)
	for name, raw := range map[string]string{
		"legacy":        `{"total":2000}`,
		"stale":         ifdLiveFixture(now.Add(-16 * time.Second)),
		"future":        ifdLiveFixture(now.Add(16 * time.Second)),
		"missing rate":  strings.Replace(fixture, `"fetch_bps":3000,`, "", 1),
		"negative":      strings.Replace(fixture, `"read_bps":2000`, `"read_bps":-1`, 1),
		"connections":   strings.Replace(fixture, `"active":4`, `"active":1.5`, 1),
		"read id":       strings.Replace(fixture, `"id":42`, `"id":"42"`, 1),
		"duplicate id":  strings.Replace(fixture, `"id":43`, `"id":42`, 1),
		"source bytes":  strings.Replace(fixture, `"bytes":1000`, `"bytes":null`, 1),
		"source rate":   strings.Replace(fixture, `"bps":300`, `"bps":-1`, 1),
		"missing reads": strings.Replace(fixture, `"reads":[`, `"other":[`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var live ifdLive
			err := json.Unmarshal([]byte(raw), &live)
			if err == nil {
				_, _, err = live.files(now)
			}
			if err == nil {
				t.Fatal("invalid live data accepted")
			}
		})
	}
}

func TestIFDV2SamplingAndFailure(t *testing.T) {
	responseBody := ifdLiveFixture(time.Now())
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ifd/api/v1/live" || r.URL.RawQuery != "" || r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("incorrect v2 request: %s", r.URL)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, responseBody)
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL + "/ifd")
	monitor := &ifdMonitor{url: endpoint, key: "test-key", client: server.Client()}
	monitor.sample(context.Background())
	if !monitor.data.Available || !monitor.data.FilesAvailable || *monitor.data.History[0].Fetched != 3000 || *monitor.data.History[0].Served != 2000 {
		t.Fatal("v2 rates must be available on the first sample")
	}
	recorder := httptest.NewRecorder()
	(&application{ifd: monitor}).ifdJSON(recorder, httptest.NewRequest(http.MethodGet, "/api/ifd", nil))
	var snapshot ifdSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Configured || !snapshot.Available || !snapshot.FilesAvailable || *snapshot.Connections != 6 || snapshot.Files[0].Name != "example.mkv" || *snapshot.Files[0].Rate != 800 || strings.Contains(recorder.Body.String(), "private") {
		t.Fatalf("incorrect dashboard snapshot: %s", recorder.Body.String())
	}
	monitor.sample(context.Background())
	if len(monitor.data.History) != 1 {
		t.Fatal("duplicate upstream sample appended")
	}
	for _, failure := range []string{"unauthorized", "legacy", "login page", "oversized"} {
		switch failure {
		case "unauthorized":
			status = http.StatusUnauthorized
		case "legacy":
			status, responseBody = http.StatusOK, `{"total":2000}`
		case "login page":
			responseBody = "<html>login</html>"
		case "oversized":
			responseBody = strings.Repeat("x", maxResponseBytes+1)
		}
		monitor.sample(context.Background())
		point := monitor.data.History[len(monitor.data.History)-1]
		if monitor.data.Available || monitor.data.FilesAvailable || monitor.data.Connections != nil || monitor.data.Files != nil || point.Fetched != nil || point.Served != nil {
			t.Fatalf("%s response did not leave an explicit gap", failure)
		}
	}
	responseBody = fmt.Sprintf(`{"at":%d,"fetch_bps":0,"read_bps":0,"providers":[],"reads":[]}`, time.Now().UnixMilli())
	monitor.sample(context.Background())
	if !monitor.data.Available || !monitor.data.FilesAvailable || len(monitor.data.Files) != 0 || *monitor.data.Connections != 0 || *monitor.data.History[len(monitor.data.History)-1].Fetched != 0 {
		t.Fatal("idle v2 sample did not recover as zero rather than unavailable")
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
	t.Setenv("IFD_URL", "http://infinidysk:3000/")
	if endpoint, _, err := loadIFDConfig(); err != nil || endpoint.String() != "http://infinidysk:3000" {
		t.Fatal("valid v2 config failed")
	}
}

func TestIFDV2SnapshotFreshness(t *testing.T) {
	monitor := &ifdMonitor{data: ifdSnapshot{Available: true, FilesAvailable: true, SampledAt: time.Now().Add(-16 * time.Second).UnixMilli(), Connections: new(float64), Files: []ifdRead{{ID: "one"}}}}
	for _, app := range []*application{{}, {ifd: monitor}} {
		recorder := httptest.NewRecorder()
		app.ifdJSON(recorder, httptest.NewRequest(http.MethodGet, "/api/ifd", nil))
		var snapshot ifdSnapshot
		if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Available || snapshot.FilesAvailable || snapshot.Connections != nil || snapshot.Files != nil || snapshot.Configured != (app.ifd != nil) {
			t.Fatalf("stale or disabled snapshot retained live data: %+v", snapshot)
		}
	}
}
