package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type User struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name      string    `gorm:"type:varchar(100);not null"`
	Email     string    `gorm:"type:varchar(255);not null;uniqueIndex"`
	Password  string    `gorm:"type:varchar(255);not null"` // berisi hash bycrypt, bukan password asli
	CreatedAt time.Time
	UpdatedAt time.Time
}

// BeforeCreate adalah hook ORM yang dipanggil otomatis sebelum INSERT
func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}
