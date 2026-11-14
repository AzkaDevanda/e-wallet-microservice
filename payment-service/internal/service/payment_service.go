package service

import (
	"context"
	"errors"
	"fmt"
	"payment-service/internal/model"
	"payment-service/internal/repository"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPaymentNotFound   = errors.New("payment not found")
	ErrPaymentExpired    = errors.New("payment has expired")
	ErrPaymentNotPending = errors.New("payment is already completed")
)

// CreatePaymentInput adalah data untuk membuat sesi pembayaran.
type CreatePaymentInput struct {
	MerchantID  string
	Amount      int64
	Description string
}

// PaymentResult adalah payment beserta saldo user saat itu.
type PaymentResult struct {
	Payment    *model.Payment
	NewBalance int64
}

type PaymentService interface {
	Create(ctx context.Context, userID uuid.UUID, input CreatePaymentInput) (*PaymentResult, error)
	Confirm(ctx context.Context, userID, paymentID uuid.UUID) (*PaymentResult, error)
	ExpireOverdue(ctx context.Context) (int64, error)
}

type paymentService struct {
	transactor  repository.Transactor
	paymentRepo repository.PaymentRepository
	wallets     WalletGateway
	ttl         time.Duration
	now         func() time.Time
}

// Confirm implements [PaymentService].
// Confirm memproses payment: potong saldo kalau masih pending dan belum lewat batas waktu.
func (s *paymentService) Confirm(ctx context.Context, userID uuid.UUID, paymentID uuid.UUID) (*PaymentResult, error) {
	var result PaymentResult
	expiredNow := false

	err := s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		// 1. Kunci baris payment, supaya confirm ganda atau worker tidak memproses bersamaan.
		payment, err := s.paymentRepo.FindByIDForUpdate(ctx, paymentID, userID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrPaymentNotFound
		}
		if err != nil {
			return fmt.Errorf("find payment: %w", err)
		}

		// 2. Hanya payment pending yang boleh diproses.
		switch payment.Status {
		case model.PaymentPending:
			// lanjut
		case model.PaymentExpired:
			return ErrPaymentExpired
		default:
			return ErrPaymentNotPending
		}

		// 3. Cek batas waktu SAAT INI, tidak bergantung pada worker.
		now := s.now()
		if payment.IsExpired(now) {
			payment.Status = model.PaymentExpired
			expiredNow = true
			return s.paymentRepo.Save(ctx, payment) // return nil → COMMIT status expired
		}

		// 4. Potong saldo + catat ledger (lewat gateway).
		newBalance, err := s.wallets.DebitForPayment(ctx, userID, payment.Amount, payment.ID, payment.MerchantID)
		if err != nil {
			return err // ROLLBACK, payment tetap pending
		}

		// 5. Tandai payment selesai.
		payment.Status = model.PaymentCompleted
		payment.PaidAt = &now
		if err := s.paymentRepo.Save(ctx, payment); err != nil {
			return fmt.Errorf("save payment: %w", err)
		}

		result = PaymentResult{Payment: payment, NewBalance: newBalance}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if expiredNow {
		return nil, ErrPaymentExpired
	}
	return &result, nil
}

// Create implements [PaymentService].
// Create membuat sesi pembayaran berstatus pending. Saldo BELUM dipotong.
func (s *paymentService) Create(ctx context.Context, userID uuid.UUID, input CreatePaymentInput) (*PaymentResult, error) {
	balance, err := s.wallets.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Tolak lebih awal kalau jelas tidak cukup. Saat confirm tetap dicek ulang.
	if balance < input.Amount {
		return nil, ErrInsufficientBalance
	}

	payment := &model.Payment{
		UserID:      userID,
		MerchantID:  strings.TrimSpace(input.MerchantID),
		Amount:      input.Amount,
		Description: strings.TrimSpace(input.Description),
		Status:      model.PaymentPending,
		ExpiresAt:   s.now().Add(s.ttl),
	}
	if err := s.paymentRepo.Create(ctx, payment); err != nil {
		return nil, fmt.Errorf("create payment: %w", err)
	}

	return &PaymentResult{Payment: payment, NewBalance: balance}, nil
}

// ExpireOverdue implements [PaymentService].
func (s *paymentService) ExpireOverdue(ctx context.Context) (int64, error) {
	return s.paymentRepo.ExpirePending(ctx, s.now())
}

func NewPaymentService(
	transactor repository.Transactor,
	paymentRepo repository.PaymentRepository,
	wallets WalletGateway,
	ttl time.Duration,
) PaymentService {
	return &paymentService{
		transactor:  transactor,
		paymentRepo: paymentRepo,
		wallets:     wallets,
		ttl:         ttl,
		now:         time.Now,
	}
}
