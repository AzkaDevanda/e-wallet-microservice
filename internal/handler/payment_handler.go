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

type PaymentHandler struct {
	paymentService service.PaymentService
}

func NewPaymentHandler(paymentService service.PaymentService) *PaymentHandler {
	return &PaymentHandler{paymentService: paymentService}
}

// Create menangani POST /transactions/pay
func (h *PaymentHandler) Create(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	var req dto.PayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	result, err := h.paymentService.Create(c.Request.Context(), userID, service.CreatePaymentInput{
		MerchantID:  req.MerchantID,
		Amount:      req.Amount,
		Description: req.Description,
	})
	if err != nil {
		h.writeError(c, "create payment", err)
		return
	}

	expiresAt := result.Payment.ExpiresAt.UTC()
	c.JSON(http.StatusOK, dto.PayResponse{
		TransactionID: result.Payment.ID.String(),
		Message:       "Payment created. Confirm before it expires.",
		NewBalance:    result.NewBalance,
		Status:        string(result.Payment.Status),
		ExpiresAt:     &expiresAt,
	})
}

// Confirm menangani POST /transactions/pay/:id/confirm
func (h *PaymentHandler) Confirm(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	paymentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "payment id must be a valid UUID"})
		return
	}

	result, err := h.paymentService.Confirm(c.Request.Context(), userID, paymentID)
	if err != nil {
		h.writeError(c, "confirm payment", err)
		return
	}

	c.JSON(http.StatusOK, dto.PayResponse{
		TransactionID: result.Payment.ID.String(),
		Message:       "Payment successful",
		NewBalance:    result.NewBalance,
		Status:        string(result.Payment.Status),
	})
}

// writeError menerjemahkan error service menjadi status code.
func (h *PaymentHandler) writeError(c *gin.Context, action string, err error) {
	switch {
	case errors.Is(err, service.ErrInsufficientBalance):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, service.ErrPaymentNotFound), errors.Is(err, service.ErrWalletNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, service.ErrPaymentExpired), errors.Is(err, service.ErrPaymentNotPending):
		c.JSON(http.StatusConflict, dto.ErrorResponse{Error: err.Error()})
	default:
		log.Printf("%s error: %v", action, err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
	}
}
