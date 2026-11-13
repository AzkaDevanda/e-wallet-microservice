package handler

import (
	"e-wallet-microservice/internal/dto"
	"e-wallet-microservice/internal/middleware"
	"e-wallet-microservice/internal/service"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TransactionHandler struct {
	transactionService service.TransactionService
}

func NewTransactionHandler(transactionService service.TransactionService) *TransactionHandler {
	return &TransactionHandler{transactionService: transactionService}
}

// TopUp menangani POST /transactions/topup
func (h *TransactionHandler) TopUp(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	var req dto.TopUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	result, err := h.transactionService.TopUp(c.Request.Context(), userID, req.Amount)
	if errors.Is(err, service.ErrWalletNotFound) {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("top up error: %v", err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusOK, dto.TopUpResponse{
		TransactionID: result.Transaction.ID.String(),
		Message:       "Top-up successful",
		NewBalance:    result.NewBalance,
	})
}

// Transfer menangani POST /transactions/transfer
func (h *TransactionHandler) Transfer(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	var req dto.TransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	toUserID, err := uuid.Parse(req.ToUserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "toUserID must be a valid UUID"})
		return
	}

	trx, err := h.transactionService.Transfer(c.Request.Context(), userID, toUserID, req.Amount)
	switch {
	case errors.Is(err, service.ErrInsufficientBalance), errors.Is(err, service.ErrSelfTransfer):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	case errors.Is(err, service.ErrRecipientNotFound), errors.Is(err, service.ErrWalletNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
		return
	case err != nil:
		log.Printf("transfer error: %v", err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusAccepted, dto.TransferResponse{
		TransactionID: trx.ID.String(),
		Status:        string(trx.Status),
		Message:       "Transfer completed successfully",
	})
}

// History menangani GET /transactions/history?page=1&limit=10
func (h *TransactionHandler) History(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	var query dto.HistoryQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	result, err := h.transactionService.History(c.Request.Context(), userID, query.Page, query.Limit)
	if err != nil {
		log.Printf("history error: %v", err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
		return
	}

	items := make([]dto.TransactionItem, 0, len(result.Items))
	for _, trx := range result.Items {
		items = append(items, dto.TransactionItem{
			TransactionID: trx.ID.String(),
			Type:          string(trx.Type),
			Amount:        trx.Amount,
			From:          trx.From,
			To:            trx.To,
			Timestamp:     trx.CreatedAt.UTC(),
			Status:        string(trx.Status),
		})
	}

	c.JSON(http.StatusOK, dto.TransactionHistoryResponse{
		Transactions: items,
		Pagination: dto.Pagination{
			CurrentPage: result.Page,
			TotalPages:  result.TotalPages,
			TotalItems:  result.TotalItems,
		},
	})
}
