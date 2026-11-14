package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrWalletNotFound      = errors.New("wallet not found")

	// ErrCoreUnavailable = core-service tidak bisa dihubungi, timeout, atau error 5xx
	// untuk debit, artinya hasilnya TIDAK DIKETAHUI: saldo bisa sudah terpotong, bisa belum
	// Payment tetap pending, dan confirm ulang aman karena debit di core idempoten
	ErrCoreUnavailable = errors.New("core service unavailable")
)

// WalletGateway adalah SATU-SATUNYA pintu payment untuk menyentuh saldo
type WalletGateway interface {
	GetBalance(ctx context.Context, userID uuid.UUID) (int64, error)
	DebitForPayment(ctx context.Context, userID uuid.UUID, amount int64, paymentID uuid.UUID, merchantID string) (int64, error)
}
