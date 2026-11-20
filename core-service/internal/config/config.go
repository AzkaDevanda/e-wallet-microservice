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

	JWTSecret string
	TokenTTL  time.Duration

	// InternalAPIKey harus SAMA dengan core-service: dikirim di header X-Internal-API-Key
	InternalAPIKey  string
	CoreServiceURL  string
	CoreHttpTimeout time.Duration

	// RabbitMQURL alamat RabbitMQ. Di docker-compose hostnya "rabbitmq", saat go run "localhost".
	RabbitMQURL string

}

// LoadConfig baca file .env dan kembaliin struct confignya
func LoadConfig() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("file .env tidak ditemukan, memakai environment variable sistem")
	}

	cfg := &Config{
		AppPort:    getEnv("APP_PORT", "8081"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5433"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "core_db"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),
		JWTSecret:  getEnv("JWT_SECRET", ""),
		TokenTTL:   getDuration("TOKEN_TTL", 24*time.Hour),

		InternalAPIKey: getEnv("INTERNAL_API_KEY", ""),

		RabbitMQURL: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),


	}

	if cfg.JWTSecret == "" {
		log.Fatal("JWT_SECRET wajib diisi")
	}
	if cfg.InternalAPIKey == "" {
		log.Fatal("INTERNAL_API_KEY wajib diisi")
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
