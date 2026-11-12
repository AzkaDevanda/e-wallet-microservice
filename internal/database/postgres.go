package database

import (
	"e-wallet-microservice/internal/config"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewPostgres buka koneksi ke postgres dan ngatur connection pool
func NewPostgres(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{TranslateError: true})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(25)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}
