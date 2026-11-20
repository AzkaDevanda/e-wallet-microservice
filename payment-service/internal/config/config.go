package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppPort string

	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// InternalAPIKey harus SAMA dengan core-service: dikirim di header X-Internal-API-Key.
	InternalAPIKey  string
	CoreServiceURL  string
	CoreHTTPTimeout time.Duration

	RabbitMQURL string

	// PaymentTTL: permintaan yang lebih tua dari ini (dihitung dari created_at di pesan) dinyatakan expired
	PaymentTTL time.Duration
	// PaymentProcessingDelay: simulasi lama pemrosesan (time.Sleep)
	PaymentProcessingDelay time.Duration
	// PaymentRetryDelay: jeda sebelum pesan yang gagal sementara (core mati) dikembalikan ke queue
	// Dibuat lebih besar dari CoreHTTPTimeout, supaya debit yang jawabannya terlambat sudah selesai di core
	// sebelum percobaan berikutnya bertanya "sudah dipotong atau belum"
	PaymentRetryDelay time.Duration
}

// LoadConfig baca file .env dan kembaliin struct confignya
func LoadConfig() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("file .env tidak ditemukan, memakai environment variable sistem")
	}

	cfg := &Config{
		AppPort:    getEnv("APP_PORT", "8082"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5434"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "payment_db"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		InternalAPIKey:  getEnv("INTERNAL_API_KEY", ""),
		CoreServiceURL:  getEnv("CORE_SERVICE_URL", "http://localhost:8081"),
		CoreHTTPTimeout: getDuration("CORE_HTTP_TIMEOUT", 5*time.Second),

		PaymentTTL: getDuration("PAYMENT_TTL", 10*time.Minute),

		RabbitMQURL: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),

		PaymentProcessingDelay: getDuration("PAYMENT_PROCESSING_DELAY", 2*time.Second),
		PaymentRetryDelay:      getDuration("PAYMENT_RETRY_DELAY", 10*time.Second),
	}

	if cfg.InternalAPIKey == "" {
		log.Fatal("INTERNAL_API_KEY wajib diisi")
	}
	if cfg.PaymentRetryDelay <= cfg.CoreHTTPTimeout {
		log.Fatalf("PAYMENT_RETRY_DELAY (%s) harus lebih besar dari CORE_HTTP_TIMEOUT (%s)",
			cfg.PaymentRetryDelay, cfg.CoreHTTPTimeout)
	}

	return cfg
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		c.DBHost, c.DBUser, c.DBPassword, c.DBName, c.DBPort, c.DBSSLMode)
}

func getEnv(key, defaultVal string) string {
	if val, exist := os.LookupEnv(key); exist && val != "" {
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
