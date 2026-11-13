package handler

import (
	"core-service/internal/dto"
	"core-service/internal/service"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// InternalWalletHandler melayani endpoint /internal/wallets/... yang dipanggil payment-service
// userID ambil dari URL (bukan dari JWT), karena pemanggilnya service, bukan user
type InternalWalletHandler struct {
	walletService service.WalletService
	debitService  service.WalletDebitService
}

func NewInternalWalletHandler(walletService service.WalletService, debitService service.WalletDebitService) *InternalWalletHandler {
	return &InternalWalletHandler{
		walletService: walletService,
		debitService:  debitService,
	}
}

// GetBalance menangani GET /internal/wallets/:userID/balance
func (h *InternalWalletHandler) GetBalance(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userID"))
	if err != nil {
		writeInternalError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid user id")
		return
	}

	wallet, err := h.walletService.GetBalance(c.Request.Context(), userID)
	if errors.Is(err, service.ErrWalletNotFound) {
		writeInternalError(c, http.StatusNotFound, "WALLET_NOT_FOUND", err.Error())
		return
	}
	if err != nil {
		log.Printf("internal get balance error: %v", err)
		writeInternalError(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "internal server error")
		return
	}

	c.JSON(http.StatusOK, dto.WalletBalanceResponse{
		UserID:  wallet.UserID.String(),
		Balance: wallet.Balance,
	})

}

// Debit menangani POST /internal/wallets/:userID/debit
func (h *InternalWalletHandler) Debit(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userID"))
	if err != nil {
		writeInternalError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid userID")
		return
	}

	var req dto.DebitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeInternalError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	referenceID, err := uuid.Parse(req.ReferenceID)
	if err != nil {
		writeInternalError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid reference id")
		return
	}

	result, err := h.debitService.Debit(c.Request.Context(), service.DebitInput{
		UserID:      userID,
		Amount:      req.Amount,
		ReferenceID: referenceID,
		MerchantID:  req.MerchantID,
	})

	switch {
	case errors.Is(err, service.ErrInsufficientBalance):
		writeInternalError(c, http.StatusBadRequest, "INSUFFICIENT_BALANCE", err.Error())
		return
	case errors.Is(err, service.ErrWalletNotFound):
		writeInternalError(c, http.StatusNotFound, "WALLET_NOT_FOUND", err.Error())
		return
	case errors.Is(err, service.ErrIdempotencyConflict):
		writeInternalError(c, http.StatusConflict, "IDEMPOTENCY_CONFLICT", err.Error())
		return
	case err != nil:
		log.Printf("internal debit error:: %v", err)
		writeInternalError(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "internal server error")
		return
	}

	// Debit baru dan replay sama-sama 200: tujuan pemanggil ("saldo terpotong untuk referenceID ini")
	// sudah tercapai. Bedanya hanya di field replayed
	c.JSON(http.StatusOK, dto.DebitResponse{
		TransactionID: result.Transaction.ID.String(),
		NewBalance:    result.NewBalance,
		Replayed:      result.Replayed,
	})
}

func writeInternalError(c *gin.Context, status int, code, message string) {
	c.JSON(status, dto.InternalErrorResponse{Error: message, Code: code})
}
