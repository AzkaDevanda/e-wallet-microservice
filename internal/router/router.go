package router

import (
	"e-wallet-microservice/internal/handler"

	"github.com/gin-gonic/gin"
)

// Deps berisi semua handler dan middleware yang dibutuhkan router.
type Deps struct {
	Health      gin.HandlerFunc
	Auth        gin.HandlerFunc
	User        *handler.UserHandler
	Wallet      *handler.WalletHandler
	Transaction *handler.TransactionHandler
	Payment     *handler.PaymentHandler
}

func New(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	api := r.Group("/api/v1")
	api.GET("/health", d.Health)

	// Public: tidak butuh token
	users := api.Group("/users")
	users.POST("/register", d.User.Register)
	users.POST("/login", d.User.Login)

	// Protected: semua route di sini melewati middleware Auth
	protected := api.Group("", d.Auth)
	protected.GET("/wallet/balance", d.Wallet.GetBalance)

	transactions := protected.Group("/transactions")
	transactions.POST("/topup", d.Transaction.TopUp)
	transactions.POST("/transfer", d.Transaction.Transfer)
	transactions.GET("/history", d.Transaction.History)

	// Payment: dipisah supaya mudah dipindah ke payment-service di Part 2
	transactions.POST("/pay", d.Payment.Create)
	transactions.POST("/pay/:id/confirm", d.Payment.Confirm)

	return r
}
