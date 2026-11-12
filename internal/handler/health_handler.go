package handler

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Health mengembalikan handler GET /health yang ikut mengecek koneksi database.
func Health(sqlDB *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := sqlDB.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "database": "DOWN"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "database": "UP"})
	}
}
