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

// ErrInvalidMessage = isi pesan tidak valid. Dicoba ulang pun tetap salah, jadi pesan dibuang
var ErrInvalidMessage = errors.New("invalid payment message")

// ProcessInput adalah isi pesan dari payment.queue (sudah di-decode oleh consumer)
type ProcessInput struct {
	PaymentID  uuid.UUID
	UserID     uuid.UUID
	MerchantID string
	Amount     int64
	CreatedAt  time.Time // waktu user menekan "bayar" di core, BUKAN waktu pesan diproses
}

// ProcessResult adalah hasil akhir yang dikirim ke status.queue
type ProcessResult struct {
	PaymentID   uuid.UUID
	Status      model.PaymentStatus // completed | expired | failed
	Reason      string
	ProcessedAt time.Time
}

type PaymentService interface {
	// Process memproses satu permintaan pembayaran. Aman dipanggil berkali-kali untuk payment yang sama
	// - error == nil			-> hasil final, kirim ke status.queue lalu ack
	// - ErrInvalidMessage	-> pesan dibuang
	// - error lain					-> gagal sementara (core mati, DB error), pesan dicoba lagi
	Process(ctx context.Context, in ProcessInput) (*ProcessResult, error)
}

type paymentService struct {
	paymentRepo     repository.PaymentRepository
	wallets         WalletGateway
	ttl             time.Duration // batas waktu permintaan (10 menit)
	processingDelay time.Duration // simulasi lama pemrosesan
	now             func() time.Time
	sleep           func(ctx context.Context, d time.Duration) error
}

// Process implements [PaymentService].
func (p *paymentService) Process(ctx context.Context, in ProcessInput) (*ProcessResult, error) {
	if err := validate(in); err != nil {
		return nil, err
	}

	// 1. Idempotensi: RabbitMQ bisa mengirim pesan yang sama
	//  lebih dari sekali (at-least-once)
	payment, err := p.paymentRepo.FindByID(ctx, in.PaymentID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		// Pertama kali: simpan sebagai pending
		payment = &model.Payment{
			ID:         in.PaymentID,
			UserID:     in.UserID,
			MerchantID: strings.TrimSpace(in.MerchantID),
			Amount:     in.Amount,
			Status:     model.PaymentPending,
			ExpiresAt:  in.CreatedAt.Add(p.ttl),
		}
		if err := p.paymentRepo.Create(ctx, payment); err != nil {
			return nil, fmt.Errorf("create payment: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("find payment: %w", err)
	case payment.UserID != in.UserID || payment.Amount != in.Amount:
		return nil, fmt.Errorf("%w: data pesan berbeda dengan payment %s yang tersimpan", ErrInvalidMessage, payment.ID)
	case payment.Status != model.PaymentPending:
		// Sudah pernah selesai diproses: jawab dengan hasil yang sama TANPA memotong saldo lagi
		return resultOf(payment), nil
	}

	// 2. Lebih dari 10 menit sejak user membayar?
	// (misalnya payment-service sempat mati lama)
	if payment.IsExpired(p.now()) {
		return p.finishOverdue(ctx, payment)
	}

	// 3. Simulasi pemrosesan (misalnya menunggu jawaban bank / payment gateway)
	if err := p.sleep(ctx, p.processingDelay); err != nil {
		return nil, err // service sedang dimatikan: pesan dikembalikan ke antrian
	}

	// 4. Potong saldo di core. referenceID = payment ID,
	// jadi kalau diulang tidak terpotong dua kali
	_, err = p.wallets.DebitForPayment(ctx, payment.UserID, payment.Amount, payment.ID, payment.MerchantID)
	switch {
	case err == nil:
		return p.finish(ctx, payment, model.PaymentCompleted, "")
	case errors.Is(err, ErrInsufficientBalance), errors.Is(err, ErrWalletNotFound):
		return p.finish(ctx, payment, model.PaymentFailed, err.Error())
	default:
		return nil, err // ErrCoreUnavailable dan error lain: coba lagi nanti
	}

}

// finishOverdue menentukan nasib payment yang sudah lewat 10 menit.
// Tanya core dulu: bisa saja percobaan sebelumnya SUDAH memotong saldo,
// tapi jawabannya tidak sampai (timeout)
func (s *paymentService) finishOverdue(ctx context.Context, payment *model.Payment) (*ProcessResult, error) {
	debit, err := s.wallets.FindDebit(ctx, payment.ID)
	switch {
	case err == nil:
		// Pengaman: referenceID unik di core, tapi tetap pastikan
		// debit ini memang untuk payment ini
		if debit.UserID != payment.UserID || debit.Amount != payment.Amount {
			return nil, fmt.Errorf("debit %s tidak cocok dengan payment %s (user/amount berbeda)",
				debit.TransactionID, payment.ID)
		}
		paidAt := debit.CreatedAt
		payment.PaidAt = &paidAt
		return s.finish(ctx, payment, model.PaymentCompleted, "")
	case errors.Is(err, ErrDebitNotFound):
		return s.finish(ctx, payment, model.PaymentExpired, "payment request older than "+s.ttl.String())
	default:
		return nil, err
	}
}

// finish menyimpan status akhir payment
func (s *paymentService) finish(ctx context.Context, payment *model.Payment, status model.PaymentStatus, reason string) (*ProcessResult, error) {
	now := s.now()
	payment.Status = status
	payment.Reason = reason
	if status == model.PaymentCompleted && payment.PaidAt == nil {
		payment.PaidAt = &now
	}
	if err := s.paymentRepo.Save(ctx, payment); err != nil {
		return nil, fmt.Errorf("save payment: %w", err)
	}
	return resultOf(payment), nil
}

func resultOf(p *model.Payment) *ProcessResult {
	return &ProcessResult{
		PaymentID:   p.ID,
		Status:      p.Status,
		Reason:      p.Reason,
		ProcessedAt: p.UpdatedAt.UTC(),
	}
}

func validate(in ProcessInput) error {
	switch {
	case in.PaymentID == uuid.Nil:
		return fmt.Errorf("%w: payment_id kosong", ErrInvalidMessage)
	case in.UserID == uuid.Nil:
		return fmt.Errorf("%w: user_id kosong", ErrInvalidMessage)
	case strings.TrimSpace(in.MerchantID) == "":
		return fmt.Errorf("%w: merchant_id kosong", ErrInvalidMessage)
	case in.Amount <= 0:
		return fmt.Errorf("%w: amount harus > 0", ErrInvalidMessage)
	case in.CreatedAt.IsZero():
		return fmt.Errorf("%w: created_at kosong", ErrInvalidMessage)
	}
	return nil
}

// sleepCtx seperti time.Sleep, tapi berhenti lebih cepat kalau ctx dibatalkan
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func NewPaymentService(
	paymentRepo repository.PaymentRepository,
	wallets WalletGateway,
	ttl time.Duration,
	processingDelay time.Duration,
) PaymentService {
	return &paymentService{
		paymentRepo:     paymentRepo,
		wallets:         wallets,
		ttl:             ttl,
		processingDelay: processingDelay,
		now:             time.Now,
		sleep:           sleepCtx,
	}
}
