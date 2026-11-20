package config

import (
	"log"
	"net/url"
	"os"
	"time"
)

type Config struct {
	AppPort string

	// Alamat service di belakang gateway. Di docker-compose: nama service (http://core-service:8081).
	CoreServiceURL *url.URL
	// UpstreamTimeout = batas waktu menunggu jawaban core.
	UpstreamTimeout time.Duration
}

// LoadConfig membaca environment variable. Gateway tidak memakai .env dan tidak butuh secret apa pun:
// ia tidak memeriksa JWT (itu tugas tiap service) dan tidak tahu INTERNAL_API_KEY.
func LoadConfig() *Config {
	return &Config{
		AppPort:         getEnv("APP_PORT", "8080"),
		CoreServiceURL:  mustURL("CORE_SERVICE_URL", "http://localhost:8081"),
		UpstreamTimeout: getDuration("UPSTREAM_TIMEOUT", 15*time.Second),
	}
}

func mustURL(key, defaultVal string) *url.URL {
	raw := getEnv(key, defaultVal)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Fatalf("%s tidak valid: %q", key, raw)
	}
	return u
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getDuration(key string, defaultVal time.Duration) time.Duration {
	d, err := time.ParseDuration(getEnv(key, ""))
	if err != nil {
		return defaultVal
	}
	return d
}
