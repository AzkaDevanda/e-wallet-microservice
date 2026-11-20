package router

import (
	"api-gateway/internal/proxy"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// upstream palsu: menjawab "<nama> <path> key=<X-Internal-API-Key>"
func fakeUpstream(name string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, name+" "+r.URL.Path+" key="+r.Header.Get("X-Internal-API-Key")+" auth="+r.Header.Get("Authorization"))
	}))
}

func newGateway(t *testing.T) *httptest.Server {
	t.Helper()
	core := fakeUpstream("core")
	t.Cleanup(core.Close)
	cu, _ := url.Parse(core.URL)
	gw := httptest.NewServer(New(Upstreams{
		Core: proxy.New("core-service", cu, 2*time.Second),
	}))
	t.Cleanup(gw.Close)
	return gw
}

func get(t *testing.T, method, u string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRouting(t *testing.T) {
	gw := newGateway(t)
	cases := []struct {
		method, path string
		status       int
		bodyPrefix   string
	}{
		{"POST", "/api/v1/transactions/topup", 200, "core "},
		{"POST", "/api/v1/transactions/pay", 200, "core /api/v1/transactions/pay "}, // Part 3: pay sekarang di core
		{"GET", "/api/v1/transactions/pay/123", 200, "core /api/v1/transactions/pay/123 "},
		{"GET", "/api/v1/transactions/history", 200, "core "},
		{"POST", "/api/v1/users/login", 200, "core "},
		{"GET", "/api/v1/health", 200, "core "},
		{"GET", "/healthz", 200, `{"status":"ok"}`},
		{"GET", "/internal/wallets/x/balance", 404, `{"error":"not found"}`},
		{"POST", "/internal/wallets/x/debit", 404, `{"error":"not found"}`},
		{"GET", "/api/v1/../internal/debits/x", 404, `{"error":"not found"}`},
		{"GET", "/api/v1/%2e%2e/internal/debits/x", 404, `{"error":"not found"}`},
		{"GET", "/api/v1//wallet/balance", 404, `{"error":"not found"}`},
		{"GET", "/", 404, `{"error":"not found"}`},
	}
	for _, c := range cases {
		// pakai request mentah supaya ".." tidak dibersihkan oleh client Go
		status, body := rawRequest(t, gw.URL, c.method, c.path)
		if status != c.status || !strings.HasPrefix(body, c.bodyPrefix) {
			t.Errorf("%s %s: got %d %q, want %d %q...", c.method, c.path, status, body, c.status, c.bodyPrefix)
		}
	}
}

func TestStripsInternalKeyAndKeepsAuthorization(t *testing.T) {
	gw := newGateway(t)
	_, body := get(t, "GET", gw.URL+"/api/v1/wallet/balance", map[string]string{
		"X-Internal-API-Key": "dicoba-dari-luar",
		"Authorization":      "Bearer abc",
	})
	if !strings.Contains(body, "key= ") || !strings.Contains(body, "auth=Bearer abc") {
		t.Fatalf("header tidak sesuai: %q", body)
	}
}

func TestUpstreamDown_Returns502JSON(t *testing.T) {
	dead, _ := url.Parse("http://127.0.0.1:1") // tidak ada yang listen
	gw := httptest.NewServer(New(Upstreams{
		Core: proxy.New("core-service", dead, time.Second),
	}))
	defer gw.Close()

	status, body := get(t, "POST", gw.URL+"/api/v1/transactions/pay", nil)
	if status != http.StatusBadGateway || body != "{\"error\":\"core-service is unavailable\"}\n" {
		t.Fatalf("got %d %q", status, body)
	}
}

func TestUpstreamSlow_Returns504(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()
	su, _ := url.Parse(slow.URL)
	gw := httptest.NewServer(New(Upstreams{
		Core: proxy.New("core-service", su, 100*time.Millisecond),
	}))
	defer gw.Close()

	if status, _ := get(t, "GET", gw.URL+"/api/v1/wallet/balance", nil); status != http.StatusGatewayTimeout {
		t.Fatalf("want 504, got %d", status)
	}
}

// rawRequest mengirim request line apa adanya (tanpa normalisasi path oleh net/http client).
func rawRequest(t *testing.T, base, method, p string) (int, string) {
	t.Helper()
	u, _ := url.Parse(base)
	req := &http.Request{Method: method, URL: &url.URL{Scheme: u.Scheme, Host: u.Host, Opaque: p}, Host: u.Host, Header: http.Header{}}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
