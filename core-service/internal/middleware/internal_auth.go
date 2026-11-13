package middleware

import (
	"core-service/internal/dto"
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

const InternalAPIKeyHeader = "X-Internal-API-Key"

func InternalAuth(apiKey string) gin.HandlerFunc {
	expected := []byte(apiKey)

	return func(ctx *gin.Context) {
		got := []byte(ctx.GetHeader(InternalAPIKeyHeader))

		// ConstantTimeCompare: lama perbandingan tidak bergantung pada beberapa karakter yang cocok
		// sehingga attacker tidak bisa menebak key sedikit demi sedikit dari waktu respons
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, dto.InternalErorResponse{
				Error: "invalid or missing internal api key",
				Code:  "UNAUTHORIZED",
			})
			return
		}
		ctx.Next()
	}
}
