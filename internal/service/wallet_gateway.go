package service

import (
	"context"
	"e-wallet-microservice/internal/model"
	"e-wallet-microservice/internal/repository"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// WalletGateway adalah SATU-SATUNYA pintu payment untuk menyentuh saldo.
// Di monolith, implementasinya langsung ke database (localWalletGateway).
// Di Part 2, interface ini diimplementasikan ulang sebagai HTTP client ke core-service.
type WalletGateway interface {
	GetBalance(ctx context.Context, userID uuid.UUID) (int64, error)
	DebitForPayment(ctx context.Context, userID uuid.UUID, amount int64, paymentID uuid.UUID, merchantID string) (int64, error)
}

type localWalletGateway struct {
	walletRepo      repository.WalletRepository
	transactionRepo repository.TransactionRepository
}

// DebitForPayment implements [WalletGateway].
// DebitForPayment memotong saldo dan mencatat ledger bertipe payment.
// Harus dipanggil di dalam Transactor.WithinTransaction supaya lock wallet bertahan.
func (g *localWalletGateway) DebitForPayment(ctx context.Context, userID uuid.UUID, amount int64, paymentID uuid.UUID, merchantID string) (int64, error) {
	wallet, err := g.walletRepo.FindByUserIDForUpdate(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return 0, ErrWalletNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock wallet: %w", err)
	}

	if wallet.Balance < amount {
		return 0, ErrInsufficientBalance
	}

	updated, err := g.walletRepo.AddBalance(ctx, userID, -amount)
	if err != nil {
		return 0, fmt.Errorf("debit wallet: %w", err)
	}

	trx := &model.Transaction{
		UserID:      userID,
		Type:        model.TransactionPayment,
		Amount:      amount,
		From:        userID.String(),
		To:          merchantID,
		Status:      model.TransactionCompleted,
		ReferenceID: &paymentID,
	}
	if err := g.transactionRepo.Create(ctx, trx); err != nil {
		return 0, fmt.Errorf("record payment transaction: %w", err)
	}

	return updated.Balance, nil
}

// GetBalance implements [WalletGateway].
func (g *localWalletGateway) GetBalance(ctx context.Context, userID uuid.UUID) (int64, error) {
	wallet, err := g.walletRepo.FindByUserID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return 0, ErrWalletNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("find wallet: %w", err)
	}
	return wallet.Balance, nil
}

func NewLocalWalletGateway(walletRepo repository.WalletRepository, transactionRepo repository.TransactionRepository) WalletGateway {
	return &localWalletGateway{walletRepo: walletRepo, transactionRepo: transactionRepo}
}
