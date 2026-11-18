package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrWalletNotFound      = errors.New("wallet not found")

	// ErrCoreUnavailable = core-service tidak bisa dihubungi, timeout, atau error 5xx
	// untuk debit, artinya hasilnya TIDAK DIKETAHUI: saldo bisa sudah terpotong, bisa belum
	// Payment tetap pending, dan confirm ulang aman karena debit di core idempoten
	ErrCoreUnavailable = errors.New("core service unavailable")

	// ErrDebitNotFound = core menjawab dengan PASTI bahwa belum ada debit untuk payment ini (Step 14)
	// Beda dengan ErrCoreUnavailable yang artinya "tidak tahu"
	ErrDebitNotFound = errors.New("debit not found")
)

// DebitRecord adalah catatan debit di ledger core untuk satu payment (Step 14)
type DebitRecord struct {
	TransactionID string
	UserID        uuid.UUID
	Amount        int64
	MerchantID    string
	CreatedAt     time.Time
	Balance       int64 // saldo user SAAT INI
}

// WalletGateway adalah SATU-SATUNYA pintu payment untuk menyentuh saldo
type WalletGateway interface {
	GetBalance(ctx context.Context, userID uuid.UUID) (int64, error)
	DebitForPayment(ctx context.Context, userID uuid.UUID, amount int64, paymentID uuid.UUID, merchantID string) (int64, error)
	// FindDebit bertanya ke core: "apakah payment ini sudah memotong saldo?" (read-only, Step 14)
	// Mengembalikan ErrDebitNotFound kalau pasti belum, ErrCoreUnavailable kalau tidak bisa dipastikan
	FindDebit(ctx context.Context, paymentID uuid.UUID) (*DebitRecord, error)
}
