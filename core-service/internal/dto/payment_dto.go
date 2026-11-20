package dto

import "time"

type PayRequest struct {
	MerchantID  string `json:"merchantID" binding:"required,max=100"`
	Amount      int64  `json:"amount" binding:"required,min=1,max=100000000"`
	Description string `json:"description" binding:"max=255"`
}

// PayResponse dikirim LANGSUNG setelah pesan masuk queue (202), saat status masih "pending"
type PayResponse struct {
	TransactionID string    `json:"transactionID"`
	Status        string    `json:"status"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"createdAt"`
}

// PaymentStatusResponse untuk GET /transactions/pay/:id
type PaymentStatusResponse struct {
	TransactionID string    `json:"transactionID"`
	MerchantID    string    `json:"merchantID"`
	Amount        int64     `json:"amount"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"`
	Reason        string    `json:"reason,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
