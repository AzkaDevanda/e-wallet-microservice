package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// internalAPIKeyHeader tidak boleh datang dari luar. Kalau client mengirimnya, gateway membuangnya.
// (Route /internal sudah diblokir di router; ini lapisan pengaman kedua.)
const internalAPIKeyHeader = "X-Internal-API-Key"

// New membuat reverse proxy ke satu service.
//   - path diteruskan apa adanya: /api/v1/wallet/balance → {target}/api/v1/wallet/balance
//   - header Authorization ikut diteruskan; JWT diperiksa oleh service tujuan
//   - kalau service mati/timeout → 502/504 dengan body JSON yang sama bentuknya dengan error API lain
func New(name string, target *url.URL, timeout time.Duration) http.Handler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout

	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)  // ganti scheme+host ke service tujuan, Host header ikut diganti
			pr.SetXForwarded() // X-Forwarded-For/Host/Proto: service tahu IP client asli
			pr.Out.Header.Del(internalAPIKeyHeader)
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			status := http.StatusBadGateway
			msg := name + " is unavailable"
			if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
				status = http.StatusGatewayTimeout
				msg = name + " did not respond in time"
			}
			if errors.Is(err, context.Canceled) {
				return // client sudah menutup koneksi; tidak ada yang perlu dijawab
			}
			log.Printf("gateway → %s %s %s: %v", name, r.Method, r.URL.Path, err)
			WriteError(w, status, msg)
		},
	}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// WriteError menulis {"error": "..."}: bentuk yang sama dengan dto.ErrorResponse di core dan payment.
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
