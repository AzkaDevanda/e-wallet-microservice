package auth

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var ErrInvalidToken = errors.New("invalid or expired token")

// JWTParser hanya MEMVERIFIKASI token. Token dibuat oleh core-service saat login
// payment service cukup memegang secret yang sama untuk mengecek tanda tangannya
type JWTParser struct {
	secret []byte
}

func NewJWTParser(secret string) *JWTParser {
	return &JWTParser{secret: []byte(secret)}
}

// Parse memverifikasi token lalu mengambil user ID di dalamnya
// Isinya sama persis dengan JWTManager.Parse di core-service
func (p *JWTParser) Parse(tokenString string) (uuid.UUID, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(t *jwt.Token) (any, error) {
			return p.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}

	return userID, nil
}
