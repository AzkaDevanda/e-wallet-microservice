package service

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// reconcileBatchSize = maksimal payment yang diperiksa worker dalam satu putaran
const reconcileBatchSize = 100

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

// ReconcileResult adalah ringkasan satu putaran rekonsiliasi (Step 14).
type ReconcileResult struct {
	Completed int // ternyata sudah dibayar di core → completed
	Expired   int // pasti belum dibayar → expired
	Skipped   int // sedang dikunci confirm / sudah tidak pending
	Failed    int // core tidak bisa dihubungi atau data tidak cocok → tetap pending, dicoba lagi nanti
}

type PaymentService interface {
	Create(ctx context.Context, userID uuid.UUID, input CreatePaymentInput) (*PaymentResult, error)
	Confirm(ctx context.Context, userID, paymentID uuid.UUID) (*PaymentResult, error)
	// ReconcileOverdue menggantikan ExpireOverdue (Step 14): payment pending yang sudah lewat
	// batas waktu tidak langsung di-expired-kan, tapi dicek dulu ke core.
	ReconcileOverdue(ctx context.Context) (ReconcileResult, error)
}

type paymentService struct {
	transactor  repository.Transactor
	paymentRepo repository.PaymentRepository
	wallets     WalletGateway
	ttl         time.Duration
	// grace: berapa lama setelah expires_at kita masih menunggu sebelum yakin "tidak ada debit".
	// Harus lebih lama dari CORE_HTTP_TIMEOUT, supaya debit yang jawabannya terlambat sempat tercatat di core.
	grace time.Duration
	now   func() time.Time
}

// overdueOutcome adalah keputusan untuk payment yang sudah lewat batas waktu.
type overdueOutcome int

const (
	outcomeCompleted overdueOutcome = iota // debit ditemukan di core
	outcomeExpired                         // debit pasti tidak ada, grace sudah lewat
	outcomeWaiting                         // debit belum ada, tapi masih dalam grace: biarkan pending
)

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
			// Step 14: JANGAN langsung tandai expired. Bisa saja confirm sebelumnya timeout,
			// padahal core sudah memotong saldo (kasus #5). Tanya core dulu.
			outcome, debit, err := s.resolveOverdue(ctx, payment, now)
			if err != nil {
				return err // core tidak bisa dihubungi: ROLLBACK, tetap pending, user boleh coba lagi
			}
			if outcome == outcomeCompleted {
				result = PaymentResult{Payment: payment, NewBalance: debit.Balance}
				return nil // COMMIT completed: uangnya memang sudah terpotong
			}
			expiredNow = true
			return nil // COMMIT (expired, atau tetap pending selama grace); jawab ke user: expired
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

// ReconcileOverdue implements [PaymentService].
// Dipanggil worker setiap interval. Hanya mengambil payment yang sudah lewat expires_at + grace,
// karena sebelum itu jawaban "debit tidak ada" dari core belum bisa dipercaya sepenuhnya.
func (s *paymentService) ReconcileOverdue(ctx context.Context) (ReconcileResult, error) {
	var res ReconcileResult

	ids, err := s.paymentRepo.ListOverduePendingIDs(ctx, s.now().Add(-s.grace), reconcileBatchSize)
	if err != nil {
		return res, fmt.Errorf("list overdue payments: %w", err)
	}

	for _, id := range ids {
		if ctx.Err() != nil {
			return res, ctx.Err() // server sedang berhenti
		}
		outcome, skipped, err := s.reconcileOne(ctx, id)
		switch {
		case err != nil:
			res.Failed++
			log.Printf("reconcile payment %s: %v (tetap pending, dicoba lagi nanti)", id, err)
		case skipped:
			res.Skipped++
		case outcome == outcomeCompleted:
			res.Completed++
			log.Printf("reconcile payment %s: debit ditemukan di core → completed", id)
		case outcome == outcomeExpired:
			res.Expired++
		default:
			res.Skipped++
		}
	}
	return res, nil
}

// reconcileOne memproses SATU payment dalam transaksinya sendiri,
// supaya satu payment yang gagal tidak membatalkan yang lain.
func (s *paymentService) reconcileOne(ctx context.Context, id uuid.UUID) (outcome overdueOutcome, skipped bool, err error) {
	err = s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		payment, err := s.paymentRepo.LockPendingByID(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			skipped = true // sedang dikunci confirm (SKIP LOCKED) atau sudah tidak pending
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock payment: %w", err)
		}

		outcome, _, err = s.resolveOverdue(ctx, payment, s.now())
		return err
	})
	return outcome, skipped, err
}

// resolveOverdue menentukan nasib payment pending yang sudah lewat expires_at, berdasarkan ledger core.
// Harus dipanggil di dalam transaksi, dengan baris payment sudah dikunci.
//   - debit ada → completed (paid_at = waktu debit di core)
//   - debit tidak ada dan grace sudah lewat → expired
//   - debit tidak ada tapi masih dalam grace → biarkan pending (debit yang terlambat mungkin masih diproses core)
//   - core tidak bisa dihubungi → error, payment tidak diubah
func (s *paymentService) resolveOverdue(ctx context.Context, payment *model.Payment, now time.Time) (overdueOutcome, *DebitRecord, error) {
	debit, err := s.wallets.FindDebit(ctx, payment.ID)
	switch {
	case err == nil:
		// Pengaman: referenceID unik di core, tapi tetap pastikan debit ini memang untuk payment ini
		if debit.UserID != payment.UserID || debit.Amount != payment.Amount {
			return 0, nil, fmt.Errorf("debit %s tidak cocok dengan payment %s (user/amount berbeda)", debit.TransactionID, payment.ID)
		}
		paidAt := debit.CreatedAt
		payment.Status = model.PaymentCompleted
		payment.PaidAt = &paidAt
		if err := s.paymentRepo.Save(ctx, payment); err != nil {
			return 0, nil, fmt.Errorf("save payment: %w", err)
		}
		return outcomeCompleted, debit, nil

	case errors.Is(err, ErrDebitNotFound):
		if now.Before(payment.ExpiresAt.Add(s.grace)) {
			return outcomeWaiting, nil, nil
		}
		payment.Status = model.PaymentExpired
		if err := s.paymentRepo.Save(ctx, payment); err != nil {
			return 0, nil, fmt.Errorf("save payment: %w", err)
		}
		return outcomeExpired, nil, nil

	default:
		return 0, nil, err
	}
}

func NewPaymentService(
	transactor repository.Transactor,
	paymentRepo repository.PaymentRepository,
	wallets WalletGateway,
	ttl time.Duration,
	grace time.Duration,
) PaymentService {
	return &paymentService{
		transactor:  transactor,
		paymentRepo: paymentRepo,
		wallets:     wallets,
		ttl:         ttl,
		grace:       grace,
		now:         time.Now,
	}
}
