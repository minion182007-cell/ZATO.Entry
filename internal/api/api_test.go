package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/api"
	"github.com/minion182007-cell/ZATO.Entry/internal/store"
	"github.com/minion182007-cell/ZATO.Entry/profiles"
	"github.com/minion182007-cell/ZATO.Entry/web"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	reg, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &api.Server{
		Profiles: reg, Plans: store.NewMemory(10), Static: web.Files(),
		Now: func() time.Time { return time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url, body string) (*http.Response, map[string]any) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return res, out
}

func TestPlanRoundTrip(t *testing.T) {
	ts := server(t)
	res, plan := post(t, ts.URL+"/api/v1/plans", `{"city":"sarov","scenario":"business"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %v", res.StatusCode, plan)
	}
	id, _ := plan["id"].(string)
	if id == "" || plan["target_date"] == "" {
		t.Fatalf("план без id или даты: %v", plan)
	}
	got, err := http.Get(ts.URL + "/api/v1/plans/" + id)
	if err != nil || got.StatusCode != http.StatusOK {
		t.Fatalf("GET план: %v %v", got.StatusCode, err)
	}
	got.Body.Close()
}

func TestBadRequests(t *testing.T) {
	ts := server(t)
	cases := []struct{ path, body string }{
		{"/api/v1/plans", `{"scenario":"nope"}`},
		{"/api/v1/plans", `{"scenario":"work","start_date":"16.11.2026"}`},
		{"/api/v1/plans", `{"scenario":"work","unknown":1}`},
		{"/api/v1/plans", `{"city":"moscow","scenario":"work"}`},
		{"/api/v1/passes/validate", `{"pass_type":"vip","persons":[]}`},
		{"/api/v1/renewals/calendar", `{"staff":[{"id":"1","pass_expires":"завтра"}]}`},
	}
	for _, c := range cases {
		res, out := post(t, ts.URL+c.path, c.body)
		if res.StatusCode != http.StatusBadRequest || out["error"] == nil {
			t.Errorf("%s %s: status %d %v", c.path, c.body, res.StatusCode, out)
		}
	}
}

func TestRenewalsICSAndStatic(t *testing.T) {
	ts := server(t)
	res, err := http.Post(ts.URL+"/api/v1/renewals/calendar?format=ics", "application/json",
		strings.NewReader(`{"staff":[{"id":"E-1","pass_expires":"2027-06-01"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "DTSTART;VALUE=DATE:20270417") {
		t.Errorf("ics: %s", body)
	}
	for _, path := range []string{"/", "/app.js", "/style.css", "/healthz", "/api/v1/profiles", "/api/v1/profiles/sarov"} {
		r, err := http.Get(ts.URL + path)
		if err != nil || r.StatusCode != http.StatusOK {
			t.Errorf("GET %s: %v", path, r.StatusCode)
		}
		r.Body.Close()
	}
}
