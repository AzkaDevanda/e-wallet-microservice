package dto

type WalletBalanceResponse struct {
	UserID  string `json:"userID"`
	Balance int64  `json:"balance"`
}
