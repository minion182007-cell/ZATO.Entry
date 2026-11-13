// Package api — HTTP API сервиса.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/passes"
	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
	"github.com/minion182007-cell/ZATO.Entry/internal/profile"
	"github.com/minion182007-cell/ZATO.Entry/internal/store"
	"github.com/minion182007-cell/ZATO.Entry/profiles"
)

const maxBody = 1 << 20 // 1 МБ

// Server — зависимости API.
type Server struct {
	Profiles *profiles.Registry
	Store    store.Store
	Static   fs.FS
	Now      func() time.Time
	Log      *slog.Logger

	requests *limiter // общий предел запросов к API с одного адреса
	signups  *limiter // предел создания кабинетов с одного адреса
}

// Handler собирает маршруты.
func (s *Server) Handler() http.Handler {
	s.requests = newLimiter(600, time.Minute)
	s.signups = newLimiter(20, time.Hour)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/profiles", s.listProfiles)
	mux.HandleFunc("GET /api/v1/profiles/{city}", s.getProfile)

	mux.HandleFunc("POST /api/v1/plans", s.createPlan)
	mux.HandleFunc("GET /api/v1/plans/{id}", s.getPlan)
	mux.HandleFunc("POST /api/v1/passes/validate", s.validatePackage)
	mux.HandleFunc("POST /api/v1/renewals/calendar", s.renewalCalendar)

	mux.HandleFunc("POST /api/v1/orgs", s.createOrg)
	mux.HandleFunc("GET /api/v1/orgs/me", s.auth(s.getOrg))
	mux.HandleFunc("GET /api/v1/orgs/me/plans", s.auth(s.listPlans))
	mux.HandleFunc("GET /api/v1/orgs/me/staff", s.auth(s.listStaff))
	mux.HandleFunc("POST /api/v1/orgs/me/staff", s.auth(s.putStaff))
	mux.HandleFunc("DELETE /api/v1/orgs/me/staff/{code}", s.auth(s.deleteStaff))
	mux.HandleFunc("POST /api/v1/orgs/me/staff/{code}/renew", s.auth(s.renewStaff))
	mux.HandleFunc("GET /api/v1/orgs/me/renewals", s.auth(s.orgRenewals))
	mux.HandleFunc("GET /api/v1/feeds/{token}/renewals.ics", s.feed)

	if s.Static != nil {
		mux.Handle("GET /", http.FileServerFS(s.Static))
	}
	return s.logging(s.headers(s.limit(mux)))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		s.Log.Error("хранилище недоступно", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- Профили ----------

func (s *Server) listProfiles(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Region string `json:"region"`
	}
	out := []item{}
	for _, p := range s.Profiles.List() {
		out = append(out, item{p.ID, p.Name, p.Region})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.Profiles.Get(r.PathValue("city"))
	if !ok {
		writeError(w, http.StatusNotFound, "профиль города не найден")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ---------- Планы ----------

type planRequest struct {
	planner.Request
	// Save — сохранить план: он получит id и будет доступен по ссылке,
	// а при запросе с ключом организации попадёт в её кабинет.
	Save bool `json:"save"`
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var req planRequest
	if !decode(w, r, &req) {
		return
	}
	p, ok := s.city(w, req.City)
	if !ok {
		return
	}
	plan, err := planner.Build(p, req.Request, s.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.Save {
		writeJSON(w, http.StatusOK, plan)
		return
	}
	orgID := ""
	if token := bearer(r); token != "" {
		org, err := s.Store.OrgByToken(r.Context(), token)
		if err != nil {
			s.storeError(w, err, "неверный ключ доступа", http.StatusUnauthorized)
			return
		}
		orgID = org.ID
	}
	if err := s.Store.SavePlan(r.Context(), plan, orgID); err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.Store.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeError(w, err, "план не найден", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// ---------- Пропуска без кабинета ----------

func (s *Server) validatePackage(w http.ResponseWriter, r *http.Request) {
	var pkg passes.Package
	if !decode(w, r, &pkg) {
		return
	}
	p, ok := s.city(w, pkg.City)
	if !ok {
		return
	}
	pt, ok := p.PassType(pkg.PassType)
	if !ok {
		writeError(w, http.StatusBadRequest, "неизвестный вид пропуска")
		return
	}
	// Персональные данные пакета не сохраняются и не пишутся в журнал.
	rep, err := passes.Validate(pt, pkg, s.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) renewalCalendar(w http.ResponseWriter, r *http.Request) {
	var req passes.RenewalRequest
	if !decode(w, r, &req) {
		return
	}
	p, ok := s.city(w, req.City)
	if !ok {
		return
	}
	cal, err := passes.BuildCalendar(p.Renewal, req, s.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.URL.Query().Get("format") == "ics" {
		writeICS(w, cal)
		return
	}
	writeJSON(w, http.StatusOK, cal)
}

// ---------- Кабинет организации ----------

type orgHandler func(w http.ResponseWriter, r *http.Request, org *store.Org)

// auth пропускает запрос с ключом доступа организации.
func (s *Server) auth(next orgHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "нужен ключ доступа организации")
			return
		}
		org, err := s.Store.OrgByToken(r.Context(), token)
		if err != nil {
			s.storeError(w, err, "неверный ключ доступа", http.StatusUnauthorized)
			return
		}
		next(w, r, org)
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) createOrg(w http.ResponseWriter, r *http.Request) {
	if !s.signups.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "слишком много новых кабинетов с этого адреса, попробуйте позже")
		return
	}
	var req struct {
		Name string `json:"name"`
		City string `json:"city"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, ok := s.city(w, req.City)
	if !ok {
		return
	}
	name, ok := cleanText(req.Name, 200)
	if !ok || name == "" {
		writeError(w, http.StatusBadRequest, "укажите название организации (до 200 символов)")
		return
	}
	org, token, err := s.Store.CreateOrg(r.Context(), name, p.ID)
	if err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	// Ключ доступа выдаётся один раз и хранится только хешем.
	writeJSON(w, http.StatusCreated, map[string]any{"org": org, "token": token})
}

func (s *Server) getOrg(w http.ResponseWriter, _ *http.Request, org *store.Org) {
	writeJSON(w, http.StatusOK, org)
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request, org *store.Org) {
	plans, err := s.Store.ListPlans(r.Context(), org.ID)
	if err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) listStaff(w http.ResponseWriter, r *http.Request, org *store.Org) {
	staff, err := s.Store.ListStaff(r.Context(), org.ID)
	if err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, staff)
}

// putStaff добавляет сотрудников или обновляет существующих по коду.
func (s *Server) putStaff(w http.ResponseWriter, r *http.Request, org *store.Org) {
	var staff []passes.Employee
	if !decode(w, r, &staff) {
		return
	}
	if len(staff) == 0 {
		writeError(w, http.StatusBadRequest, "пустой список сотрудников")
		return
	}
	for i := range staff {
		e := &staff[i]
		var okID, okName bool
		e.ID, okID = cleanText(e.ID, 64)
		e.Name, okName = cleanText(e.Name, 200)
		if !okID || e.ID == "" || strings.ContainsAny(e.ID, "/;") {
			writeError(w, http.StatusBadRequest, rowError(i, "код обязателен: до 64 символов, без «/» и «;»"))
			return
		}
		if !okName {
			writeError(w, http.StatusBadRequest, rowError(i, "имя — до 200 символов"))
			return
		}
		if _, err := time.Parse("2006-01-02", e.PassExpires); err != nil {
			writeError(w, http.StatusBadRequest, rowError(i, "срок пропуска — дата в формате ГГГГ-ММ-ДД"))
			return
		}
	}
	if err := s.Store.PutStaff(r.Context(), org.ID, staff); err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	s.orgRenewals(w, r, org)
}

func (s *Server) deleteStaff(w http.ResponseWriter, r *http.Request, org *store.Org) {
	if err := s.Store.DeleteStaff(r.Context(), org.ID, r.PathValue("code")); err != nil {
		s.storeError(w, err, "сотрудник не найден", http.StatusNotFound)
		return
	}
	s.orgRenewals(w, r, org)
}

// renewStaff отмечает продление: новый срок — из запроса либо прежний
// срок плюс срок действия пропуска из профиля города.
func (s *Server) renewStaff(w http.ResponseWriter, r *http.Request, org *store.Org) {
	var req struct {
		PassExpires string `json:"pass_expires"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	p, ok := s.city(w, org.City)
	if !ok {
		return
	}
	e, err := s.Store.GetStaff(r.Context(), org.ID, r.PathValue("code"))
	if err != nil {
		s.storeError(w, err, "сотрудник не найден", http.StatusNotFound)
		return
	}
	if req.PassExpires != "" {
		if _, err := time.Parse("2006-01-02", req.PassExpires); err != nil {
			writeError(w, http.StatusBadRequest, "срок пропуска — дата в формате ГГГГ-ММ-ДД")
			return
		}
		e.PassExpires = req.PassExpires
	} else {
		old, _ := time.Parse("2006-01-02", e.PassExpires)
		e.PassExpires = old.AddDate(0, 0, p.Renewal.ValidDays).Format("2006-01-02")
	}
	if err := s.Store.PutStaff(r.Context(), org.ID, []passes.Employee{*e}); err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	s.orgRenewals(w, r, org)
}

func (s *Server) orgCalendar(r *http.Request, org *store.Org) (*passes.RenewalCalendar, error) {
	p, ok := s.Profiles.Get(org.City)
	if !ok {
		return nil, errors.New("профиль города организации не найден")
	}
	staff, err := s.Store.ListStaff(r.Context(), org.ID)
	if err != nil {
		return nil, err
	}
	return passes.BuildCalendar(p.Renewal, passes.RenewalRequest{Staff: staff}, s.Now())
}

func (s *Server) orgRenewals(w http.ResponseWriter, r *http.Request, org *store.Org) {
	cal, err := s.orgCalendar(r, org)
	if err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, cal)
}

// feed отдаёт календарь продлений для подписки из почтовых и
// календарных программ. Ключ в адресе даёт доступ только на чтение.
func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	org, err := s.Store.OrgByFeedToken(r.Context(), r.PathValue("token"))
	if err != nil {
		s.storeError(w, err, "календарь не найден", http.StatusNotFound)
		return
	}
	cal, err := s.orgCalendar(r, org)
	if err != nil {
		s.storeError(w, err, "", 0)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=900")
	writeICS(w, cal)
}

// ---------- Вспомогательное ----------

func (s *Server) city(w http.ResponseWriter, id string) (*profile.Profile, bool) {
	if id == "" {
		id = "sarov"
	}
	p, ok := s.Profiles.Get(id)
	if !ok {
		writeError(w, http.StatusBadRequest, "неизвестный город")
	}
	return p, ok
}

// storeError отвечает notFoundMsg/notFoundStatus на ErrNotFound и не
// раскрывает клиенту внутренние ошибки хранилища.
func (s *Server) storeError(w http.ResponseWriter, err error, notFoundMsg string, notFoundStatus int) {
	switch {
	case errors.Is(err, store.ErrNotFound) && notFoundStatus != 0:
		writeError(w, notFoundStatus, notFoundMsg)
	case errors.Is(err, store.ErrLimit):
		writeError(w, http.StatusUnprocessableEntity, "превышен предел числа сотрудников в организации")
	default:
		s.Log.Error("ошибка хранилища", "err", err)
		writeError(w, http.StatusInternalServerError, "внутренняя ошибка, попробуйте позже")
	}
}

// cleanText обрезает пробелы и отклоняет управляющие символы и
// слишком длинные строки.
func cleanText(v string, max int) (string, bool) {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max {
		return "", false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return v, true
}

func rowError(i int, msg string) string {
	return "сотрудник №" + strconv.Itoa(i+1) + ": " + msg
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "слишком большой запрос")
		} else {
			writeError(w, http.StatusBadRequest, "некорректный JSON: "+err.Error())
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeICS(w http.ResponseWriter, cal *passes.RenewalCalendar) {
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="renewals.ics"`)
	_, _ = w.Write([]byte(cal.ICS()))
}
