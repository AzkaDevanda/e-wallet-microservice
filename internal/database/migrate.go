package database

import (
	"e-wallet-microservice/internal/model"

	"gorm.io/gorm"
)

// Migrate membuat atau memperbarui tabel sesuai struct model.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{},
		&model.Wallet{},
		&model.Transaction{},
		&model.Payment{},
	)
}
