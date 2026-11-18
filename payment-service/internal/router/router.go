package router

import (
	"payment-service/internal/handler"

	"github.com/gin-gonic/gin"
)

type Deps struct {
	Health  gin.HandlerFunc
	Auth    gin.HandlerFunc
	Payment *handler.PaymentHandler
}

func New(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	api := r.Group("/api/v1")
	api.GET("/health", d.Health)

	// Path SAMA dengan Part 1, supaya nanti gateway cukup meneruskan apa adanya.
	transactions := api.Group("/transactions", d.Auth)
	transactions.POST("/pay", d.Payment.Create)
	transactions.POST("/pay/:id/confirm", d.Payment.Confirm)

	return r

}
