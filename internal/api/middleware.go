package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter — счётчик запросов по адресу в фиксированном окне.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	start  time.Time
	counts map[string]int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, start: time.Now(), counts: map[string]int{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.start) > l.window {
		l.start, l.counts = time.Now(), map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= l.limit
}

// clientIP — адрес соединения. За обратным прокси здесь будет адрес
// прокси: ограничение по клиентам тогда настраивается на прокси.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.requests.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "слишком много запросов, попробуйте через минуту")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// headers добавляет защитные заголовки. Интерфейс не использует
// встроенных скриптов и стилей, поэтому политика строгая.
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// logging пишет метод, путь и статус. Ключ календаря в пути скрывается;
// тела запросов в журнал не попадают.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/v1/feeds/") {
			path = "/api/v1/feeds/***/renewals.ics"
		}
		s.Log.Info("запрос", "method", r.Method, "path", path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}
