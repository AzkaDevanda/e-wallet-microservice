package router

import (
	"github.com/gin-gonic/gin"
)

type Deps struct {
	Health gin.HandlerFunc
}

// payment-service tidak lagi menerima request dari user (semua lewat RabbitMQ)
// HTTP server hanya untuk health check
func New(d Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	api := r.Group("/api/v1")
	api.GET("/health", d.Health)

	return r

}
