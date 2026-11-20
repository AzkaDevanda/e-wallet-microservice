package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PaymentStatus string

const (
	PaymentPending   PaymentStatus = "pending"
	PaymentCompleted PaymentStatus = "completed"
	PaymentExpired   PaymentStatus = "expired"
	PaymentFailed    PaymentStatus = "failed"
)

type Payment struct {
	ID          uuid.UUID     `gorm:"type:uuid;primaryKey"` // payment_id dari pesan RabbitMQ
	UserID      uuid.UUID     `gorm:"type:uuid;not null;index"`
	MerchantID  string        `gorm:"type:varchar(100);not null"`
	Amount      int64         `gorm:"not null;check:chk_payment_amount_positive,amount>0"`
	Description string        `gorm:"type:varchar(255)"`
	Status      PaymentStatus `gorm:"type:varchar(20);not null;index:idx_payments_status_expires,priority:1"`
	Reason      string        `gorm:"type:varchar(255);not null;default:''"`
	ExpiresAt   time.Time     `gorm:"not null;index:idx_payments_status_expires,priority:2"`
	PaidAt      *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (p *Payment) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}

// IsExpired true kalau waktu sekarang sudah sama dengan atau melewati ExpiresAt.
func (p *Payment) IsExpired(now time.Time) bool {
	return !now.Before(p.ExpiresAt)
}
