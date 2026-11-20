package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PaymentRequestStatus string

const (
	PaymentRequestPending   PaymentRequestStatus = "pending"   // sudah masuk antrian, belum diproses
	PaymentRequestCompleted PaymentRequestStatus = "completed" // saldo terpotong
	PaymentRequestExpired   PaymentRequestStatus = "expired"   // lebih dari 10 menit belum diproses
	PaymentRequestFailed    PaymentRequestStatus = "failed"    // saldo kurang, wallet tidak ada, atau gagal masuk queue
)

// IsFinal true untuk status hasil akhir pemrosesan (yang boleh datang dari status.queue)
func (s PaymentRequestStatus) IsFinal() bool {
	switch s {
	case PaymentRequestCompleted, PaymentRequestExpired, PaymentRequestFailed:
		return true
	}
	return false
}

// PaymentRequest adalah catatan core tentang permintaan pembayaran yang dikirim ke payment-service lewat RabbitMQ.
// Yang memotong saldo tetap endpoint internal debit (ledger di tabel transactions);
// tabel ini hanya untuk melacak status permintaan dan menjawab GET /transactions/pay/:id.
type PaymentRequest struct {
	ID          uuid.UUID            `gorm:"type:uuid;primaryKey"`
	UserID      uuid.UUID            `gorm:"type:uuid;not null;index"`
	MerchantID  string               `gorm:"type:varchar(100);not null"`
	Amount      int64                `gorm:"not null;check:chk_payment_request_amount_positive,amount>0"`
	Description string               `gorm:"type:varchar(255);not null;default:''"`
	Status      PaymentRequestStatus `gorm:"type:varchar(20);not null"`
	Reason      string               `gorm:"type:varchar(255);not null;default:''"`
	CreatedAt   time.Time            // diisi service = created_at di pesan RabbitMQ
	UpdatedAt   time.Time
}

func (p *PaymentRequest) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return nil
}
