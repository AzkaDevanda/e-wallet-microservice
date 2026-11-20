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
	// Health dipanggil Docker setiap 10 detik; tidak perlu dicatat di log.
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{SkipPaths: []string{"/api/v1/health"}}), gin.Recovery())

	api := r.Group("/api/v1")
	api.GET("/health", d.Health)

	return r

}
