package messaging

import (
	"time"

	"github.com/google/uuid"
)

// Nama-nama di RabbitMQ. HARUS sama persis dengan yang ada di service lain
const (
	ExchangePayment = "payment.exchange" // direct exchange
	QueuePayment    = "payment.queue"    // core -> payment: permintaan pembayaran
	QueueStatus     = "status.queue"     // payment -> core: hasil pemrosesan

	RoutingKeyPayment = "payment.request"
	RoutingKeyStatus  = "payment.status"
)

// PaymentRequestMessage adalah isi pesan di payment.queue
// uuid.UUID otomatis jadi string, time.Time otomatis jadi
// RFC3339 saat dimarshal oleh JSON nanti
type PaymentRequestMessage struct {
	PaymentID  uuid.UUID `json:"payment_id"`
	UserID     uuid.UUID `json:"user_id"`
	MerchantID string    `json:"merchant_id"`
	Amount     int64     `json:"amount"`
	CreatedAt  time.Time `json:"created_at"`
}

type PaymentStatusMessage struct {
	PaymentID   uuid.UUID `json:"payment_id"`
	Status      string    `json:"status"` // completed / expired / failed
	Reason      string    `json:"reason,omitempty"`
	ProcessedAt time.Time `json:"processed_at"`
}
