package handler

import (
	"core-service/internal/dto"
	"core-service/internal/middleware"
	"core-service/internal/service"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type PaymentHandler struct {
	paymentService service.PaymentRequestService
}

func NewPaymentHandler(paymentService service.PaymentRequestService) *PaymentHandler {
	return &PaymentHandler{paymentService: paymentService}
}

// Pay menangani POST /transactions/pay
// Tidak menunggu pembayaran selesai: pesan dikirim ke
// RabbitMQ, lalu langsung membalas 202 "pending"
func (h *PaymentHandler) Pay(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized,
			dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	var req dto.PayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: err.Error()})
		return
	}

	pr, err := h.paymentService.Create(c.Request.Context(),
		userID, service.CreatePaymentInput{
			MerchantID:  req.MerchantID,
			Amount:      req.Amount,
			Description: req.Description,
		})

	switch {
	case errors.Is(err, service.ErrInsufficientBalance):
		c.JSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: err.Error()})
		return
	case errors.Is(err, service.ErrWalletNotFound):
		c.JSON(http.StatusNotFound,
			dto.ErrorResponse{Error: err.Error()})
		return
	case errors.Is(err, service.ErrBrokerUnavailable):
		c.JSON(http.StatusServiceUnavailable,
			dto.ErrorResponse{Error: err.Error()})
		return
	case err != nil:
		log.Printf("pay error :%v", err)
		c.JSON(http.StatusInternalServerError,
			dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusAccepted, dto.PayResponse{
		TransactionID: pr.ID.String(),
		Status:        string(pr.Status),
		Message:       "Payment is being processed",
		CreatedAt:     pr.CreatedAt.UTC(),
	})
}

// GetStatus menangani GET /transactions/pay/:id
func (h *PaymentHandler) GetStatus(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized,
			dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "payment id must be a valid UUID"})
		return
	}

	pr, err := h.paymentService.Get(c.Request.Context(), userID, id)
	if errors.Is(err, service.ErrPaymentNotFound) {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
		return
	}

	if err != nil {
		log.Printf("get payment error: %v", err)
		c.JSON(http.StatusInternalServerError,
			dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusOK, dto.PaymentStatusResponse{
		TransactionID: pr.ID.String(),
		MerchantID:    pr.MerchantID,
		Amount:        pr.Amount,
		Description:   pr.Description,
		Status:        string(pr.Status),
		Reason:        pr.Reason,
		CreatedAt:     pr.CreatedAt.UTC(),
		UpdatedAt:     pr.UpdatedAt.UTC(),
	})

}
