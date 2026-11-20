package router

import (
	"api-gateway/internal/proxy"
	"log"
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	apiPrefix  = "/api/v1/"
	healthPath = "/healthz"
)

// Upstreams adalah handler proxy untuk setiap service.
// Payment-service tidak lagi menerima request dari luar (ia hanya consumer RabbitMQ),
// jadi gateway cukup meneruskan semua /api/v1/... ke core-service.
type Upstreams struct {
	Core http.Handler
}

// New membuat handler gateway. Aturan routing (urutan penting):
//
//	/healthz                         → dijawab gateway sendiri
//	path tidak bersih (.., //)       → 404 (mencegah /api/v1/../internal/...)
//	/api/v1/...                      → core-service (termasuk /transactions/pay)
//	lainnya, termasuk /internal/...  → 404 (endpoint internal TIDAK PERNAH dibuka ke luar)
func New(u Upstreams) http.Handler {
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		switch {
		case p == healthPath && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case !isClean(p):
			proxy.WriteError(w, http.StatusNotFound, "not found")
		case strings.HasPrefix(p, apiPrefix):
			u.Core.ServeHTTP(w, r)
		default:
			proxy.WriteError(w, http.StatusNotFound, "not found")
		}
	})
	return logRequests(mux)
}

// Target memberi tahu ke mana sebuah path akan diteruskan ("core" atau ""). Dipakai log dan test.
func Target(p string) string {
	switch {
	case p == healthPath || !isClean(p):
		return ""
	case strings.HasPrefix(p, apiPrefix):
		return "core"
	default:
		return ""
	}
}

// isClean: path sudah dalam bentuk paling sederhana (tanpa "..", ".", atau "//").
// URL.Path sudah di-decode, jadi %2e%2e juga tertangkap di sini.
func isClean(p string) bool {
	return p != "" && path.Clean(p) == p
}

// statusRecorder mencatat status code untuk log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		target := Target(r.URL.Path)
		if target == "" {
			target = "gateway"
		}
		log.Printf("%s %s → %s %d (%s)", r.Method, r.URL.Path, target, rec.status, time.Since(start).Round(time.Millisecond))
	})
}
