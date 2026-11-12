package middleware

import (
	"e-wallet-microservice/internal/dto"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TokenParser adalah kontrak yang dibutuhkan middleware untuk memeriksa JWT.
type TokenParser interface {
	Parse(tokenString string) (uuid.UUID, error)
}

type userIDKey struct{}

// Auth memastikan request membawa JWT yang valid, lalu menyimpan user ID ke gin.Context.
func Auth(parser TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")

		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{
				Error: "missing or invalid authorization header",
			})
			return
		}

		userID, err := parser.Parse(strings.TrimSpace(token))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: err.Error()})
			return
		}

		c.Set(userIDKey{}, userID)
		c.Next()
	}
}

// UserIDFrom mengambil user ID yang sudah disimpan oleh middleware Auth.
func UserIDFrom(c *gin.Context) (uuid.UUID, bool) {
	value, exists := c.Get(userIDKey{})
	if !exists {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok
}
