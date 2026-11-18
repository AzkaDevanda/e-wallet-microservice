package dto

import "time"

type TopUpRequest struct {
	Amount int64 `json:"amount" binding:"required,min=1,max=100000000"`
}

type TopUpResponse struct {
	TransactionID string `json:"transactionID"`
	Message       string `json:"message"`
	NewBalance    int64  `json:"newBalance"`
}

type TransferRequest struct {
	ToUserID string `json:"toUserID" binding:"required,uuid"`
	Amount   int64  `json:"amount" binding:"required,min=1,max=100000000"`
}

type TransferResponse struct {
	TransactionID string `json:"transactionID"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}

type HistoryQuery struct {
	Page  int `form:"page,default=1" binding:"min=1"`
	Limit int `form:"limit,default=10" binding:"min=1,max=100"`
}

type TransactionItem struct {
	TransactionID string    `json:"transactionID"`
	Type          string    `json:"type"`
	Amount        int64     `json:"amount"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	Timestamp     time.Time `json:"timestamp"`
	Status        string    `json:"status"`
}

type Pagination struct {
	CurrentPage int   `json:"currentPage"`
	TotalPages  int   `json:"totalPages"`
	TotalItems  int64 `json:"totalItems"`
}

type TransactionHistoryResponse struct {
	Transactions []TransactionItem `json:"transactions"`
	Pagination   Pagination        `json:"pagination"`
}
