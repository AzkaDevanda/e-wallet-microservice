package dto

type InternalErorResponse struct {
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


