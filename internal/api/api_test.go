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
		Profiles: reg, Store: store.NewMemory(), Static: web.Files(),
		Now: func() time.Time { return time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// call выполняет запрос и разбирает JSON-ответ (объект или массив).
func call(t *testing.T, method, url, token, body string) (int, any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out any
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, url, err, raw)
		}
	}
	return res.StatusCode, out, string(raw)
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func TestPlanIsSavedOnlyOnRequest(t *testing.T) {
	ts := server(t)
	status, out, _ := call(t, "POST", ts.URL+"/api/v1/plans", "", `{"scenario":"business"}`)
	if status != http.StatusOK || obj(out)["id"] != nil || obj(out)["target_date"] == "" {
		t.Fatalf("расчёт без сохранения: %d %v", status, out)
	}
	status, out, _ = call(t, "POST", ts.URL+"/api/v1/plans", "", `{"scenario":"business","save":true}`)
	id, _ := obj(out)["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("сохранение: %d %v", status, out)
	}
	status, out, _ = call(t, "GET", ts.URL+"/api/v1/plans/"+id, "", "")
	if status != http.StatusOK || obj(out)["scenario"] != "business" {
		t.Fatalf("чтение плана: %d %v", status, out)
	}
	if status, _, _ := call(t, "GET", ts.URL+"/api/v1/plans/nope", "", ""); status != http.StatusNotFound {
		t.Errorf("нет плана: %d", status)
	}
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
		{"/api/v1/orgs", `{"name":"  "}`},
		{"/api/v1/orgs", `{"name":"А","city":"moscow"}`},
	}
	for _, c := range cases {
		status, out, _ := call(t, "POST", ts.URL+c.path, "", c.body)
		if status != http.StatusBadRequest || obj(out)["error"] == nil {
			t.Errorf("%s %s: status %d %v", c.path, c.body, status, out)
		}
	}
}

func TestOrgCabinet(t *testing.T) {
	ts := server(t)
	base := ts.URL + "/api/v1"

	status, out, _ := call(t, "POST", base+"/orgs", "", `{"name":"ООО «Пример»"}`)
	token, _ := obj(out)["token"].(string)
	feed, _ := obj(obj(out)["org"])["feed_token"].(string)
	if status != http.StatusCreated || !strings.HasPrefix(token, "zk_") || feed == "" {
		t.Fatalf("создание кабинета: %d %v", status, out)
	}
	_, out2, _ := call(t, "POST", base+"/orgs", "", `{"name":"Чужая"}`)
	foreign, _ := obj(out2)["token"].(string)

	// Без ключа и с неверным ключом кабинет закрыт.
	for _, tok := range []string{"", "zk_wrong", feed} {
		for _, path := range []string{"/orgs/me", "/orgs/me/staff", "/orgs/me/plans", "/orgs/me/renewals"} {
			if status, _, _ := call(t, "GET", base+path, tok, ""); status != http.StatusUnauthorized {
				t.Errorf("GET %s с ключом %q: %d", path, tok, status)
			}
		}
	}

	// Штат: добавление, проверка данных, календарь.
	bad := []string{
		`[]`,
		`[{"id":"","pass_expires":"2027-01-01"}]`,
		`[{"id":"a/b","pass_expires":"2027-01-01"}]`,
		`[{"id":"E-1","pass_expires":"01.01.2027"}]`,
	}
	for _, body := range bad {
		if status, _, raw := call(t, "POST", base+"/orgs/me/staff", token, body); status != http.StatusBadRequest {
			t.Errorf("штат %s: %d %s", body, status, raw)
		}
	}
	status, out, _ = call(t, "POST", base+"/orgs/me/staff", token,
		`[{"id":"С-001","name":"Монтажник","pass_expires":"2027-01-05"},{"id":"С-002","pass_expires":"2027-06-01"}]`)
	if status != http.StatusOK || len(obj(out)["items"].([]any)) != 2 {
		t.Fatalf("добавление штата: %d %v", status, out)
	}
	first := obj(obj(out)["items"].([]any)[0])
	if first["id"] != "С-001" || first["submit_by"] != "2026-11-21" || first["status"] != "due" {
		t.Errorf("календарь: %v", first)
	}

	// Чужая организация не видит и не меняет штат.
	_, out, _ = call(t, "GET", base+"/orgs/me/staff", foreign, "")
	if list, _ := out.([]any); len(list) != 0 {
		t.Errorf("чужой штат виден: %v", out)
	}
	if status, _, _ := call(t, "DELETE", base+"/orgs/me/staff/С-001", foreign, ""); status != http.StatusNotFound {
		t.Errorf("удаление чужого сотрудника: %d", status)
	}

	// Продление: прежний срок + 365 дней.
	status, _, _ = call(t, "POST", base+"/orgs/me/staff/С-001/renew", token, `{}`)
	_, out, _ = call(t, "GET", base+"/orgs/me/staff", token, "")
	if got := obj(out.([]any)[0])["pass_expires"]; status != http.StatusOK || got != "2028-01-05" {
		t.Errorf("продление: %d, срок %v", status, got)
	}
	status, _, _ = call(t, "POST", base+"/orgs/me/staff/С-001/renew", token, `{"pass_expires":"2028-03-01"}`)
	_, out, _ = call(t, "GET", base+"/orgs/me/staff", token, "")
	if got := obj(out.([]any)[0])["pass_expires"]; status != http.StatusOK || got != "2028-03-01" {
		t.Errorf("продление с датой: %d, срок %v", status, got)
	}

	// Подписка на календарь по ключу только для чтения.
	status, _, ics := call(t, "GET", base+"/feeds/"+feed+"/renewals.ics", "", "")
	if status != http.StatusOK || strings.Count(ics, "BEGIN:VEVENT") != 2 {
		t.Errorf("календарь по подписке: %d\n%s", status, ics)
	}
	if status, _, _ := call(t, "GET", base+"/feeds/"+token+"/renewals.ics", "", ""); status != http.StatusNotFound {
		t.Errorf("ключ доступа принят как ключ календаря: %d", status)
	}

	if status, _, _ := call(t, "DELETE", base+"/orgs/me/staff/С-002", token, ""); status != http.StatusOK {
		t.Errorf("удаление: %d", status)
	}

	// Сохранённый с ключом план попадает в кабинет.
	call(t, "POST", base+"/plans", token, `{"scenario":"work","save":true}`)
	call(t, "POST", base+"/plans", "", `{"scenario":"visit","save":true}`)
	_, out, _ = call(t, "GET", base+"/orgs/me/plans", token, "")
	if list, _ := out.([]any); len(list) != 1 || obj(list[0])["scenario"] != "work" {
		t.Errorf("планы кабинета: %v", out)
	}
	if status, _, _ := call(t, "POST", base+"/plans", "zk_wrong", `{"scenario":"work","save":true}`); status != http.StatusUnauthorized {
		t.Errorf("сохранение с неверным ключом: %d", status)
	}
}

func TestStaticHeadersAndICS(t *testing.T) {
	ts := server(t)
	_, _, ics := call(t, "POST", ts.URL+"/api/v1/renewals/calendar?format=ics", "",
		`{"staff":[{"id":"E-1","pass_expires":"2027-06-01"}]}`)
	if !strings.Contains(ics, "DTSTART;VALUE=DATE:20270417") {
		t.Errorf("ics: %s", ics)
	}
	for _, path := range []string{"/", "/app.js", "/style.css", "/healthz", "/api/v1/profiles", "/api/v1/profiles/sarov"} {
		r, err := http.Get(ts.URL + path)
		if err != nil || r.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v", path, err)
		}
		if r.Header.Get("Content-Security-Policy") == "" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: нет защитных заголовков", path)
		}
		r.Body.Close()
	}
}
