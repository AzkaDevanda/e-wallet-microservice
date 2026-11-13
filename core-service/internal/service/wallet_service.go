package service

import (
	"context"
	"e-wallet-microservice/internal/model"
	"e-wallet-microservice/internal/repository"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrWalletNotFound = errors.New("wallet not found")

type WalletService interface {
	GetBalance(ctx context.Context, userID uuid.UUID) (*model.Wallet, error)
}

type walletService struct {
	walletRepo repository.WalletRepository
}

func NewWalletService(walletRepo repository.WalletRepository) WalletService {
	return &walletService{walletRepo: walletRepo}
}

// GetBalance implements [WalletService].
func (s *walletService) GetBalance(ctx context.Context, userID uuid.UUID) (*model.Wallet, error) {
	wallet, err := s.walletRepo.FindByUserID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find wallet: %w", err)
	}
	return wallet, nil
}
