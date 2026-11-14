package dto

import "time"

type PayRequest struct {
	MerchantID  string `json:"merchantID" binding:"required,max=100"`
	Amount      int64  `json:"amount" binding:"required,min=1,max=100000000"`
	Description string `json:"description" binding:"max=255"`
}

// PayResponse mengikuti schema PayResponse di spec, ditambah status dan expiresAt.
type PayResponse struct {
	TransactionID string     `json:"transactionID"`
	Message       string     `json:"message"`
	NewBalance    int64      `json:"newBalance"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
}
