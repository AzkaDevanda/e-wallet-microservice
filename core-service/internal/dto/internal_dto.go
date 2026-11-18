package dto

import "time"

type InternalErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type DebitRequest struct {
	Amount      int64  `json:"amount" binding:"required,min=1,max=100000000"`
	ReferenceID string `json:"referenceID" binding:"required,uuid"`
	MerchantID  string `json:"merchantID" binding:"required,max=100"`
}

type DebitResponse struct {
	TransactionID string `json:"transactionID"`
	NewBalance    int64  `json:"newBalance"`
	Replayed      bool   `json:"replayed"`
}

// DebitRecordResponse adalah jawaban GET /internal/debits/:referenceID (Step 14).
// Balance = saldo wallet SAAT INI (bukan saldo tepat setelah debit), supaya payment-service
// bisa membalas confirm tanpa memanggil endpoint balance lagi.
type DebitRecordResponse struct {
	TransactionID string    `json:"transactionID"`
	UserID        string    `json:"userID"`
	Amount        int64     `json:"amount"`
	MerchantID    string    `json:"merchantID"`
	CreatedAt     time.Time `json:"createdAt"`
	Balance       int64     `json:"balance"`
}
