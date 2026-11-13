// Package api — HTTP API сервиса.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
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
	Plans    store.Plans
	Static   fs.FS
	Now      func() time.Time
	Log      *slog.Logger
}

// Handler собирает маршруты.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/profiles", s.listProfiles)
	mux.HandleFunc("GET /api/v1/profiles/{city}", s.getProfile)
	mux.HandleFunc("POST /api/v1/plans", s.createPlan)
	mux.HandleFunc("GET /api/v1/plans/{id}", s.getPlan)
	mux.HandleFunc("POST /api/v1/passes/validate", s.validatePackage)
	mux.HandleFunc("POST /api/v1/renewals/calendar", s.renewalCalendar)
	if s.Static != nil {
		mux.Handle("GET /", http.FileServerFS(s.Static))
	}
	return s.logging(mux)
}

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

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var req planner.Request
	if !decode(w, r, &req) {
		return
	}
	p, ok := s.city(w, req.City)
	if !ok {
		return
	}
	plan, err := planner.Build(p, req, s.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.Plans.Save(plan); err != nil {
		s.Log.Error("сохранение плана", "err", err)
		writeError(w, http.StatusInternalServerError, "не удалось сохранить план")
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.Plans.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "план не найден")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

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
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="renewals.ics"`)
		_, _ = w.Write([]byte(cal.ICS()))
		return
	}
	writeJSON(w, http.StatusOK, cal)
}

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

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.Log.Info("запрос", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}
