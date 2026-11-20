package handler

import (
	"core-service/internal/dto"
	"core-service/internal/middleware"
	"core-service/internal/service"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type WalletHandler struct {
	walletService service.WalletService
}

func NewWalletHandler(walletService service.WalletService) *WalletHandler {
	return &WalletHandler{walletService: walletService}
}

// GetBalance menangani GET /wallet/balance
func (h *WalletHandler) GetBalance(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return
	}

	wallet, err := h.walletService.GetBalance(c.Request.Context(), userID)
	if errors.Is(err, service.ErrWalletNotFound) {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("get balance error: %v", err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusOK, dto.WalletBalanceResponse{
		UserID:  wallet.UserID.String(),
		Balance: wallet.Balance,
	})
}
