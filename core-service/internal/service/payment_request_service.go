package service

import (
	"context"
	"core-service/internal/messaging"
	"core-service/internal/model"
	"core-service/internal/repository"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPaymentNotFound = errors.New("payment not found")

	// ErrBrokerUnavailable = RabbitMQ tidak bisa dihubungi / tidak mengonfirmasi pesan. Aman untuk dicoba lagi
	ErrBrokerUnavailable = errors.New("payment queue is temporarily unavailable")
)

// PaymentPublisher adalah kontrak untuk mengirim pesan ke payment.queue (dipenuhi oleh messaging.Publisher)
type PaymentPublisher interface {
	PublishPaymentRequest(ctx context.Context, msg messaging.PaymentRequestMessage) error
}

type CreatePaymentInput struct {
	MerchantID  string
	Amount      int64
	Description string
}

type PaymentRequestService interface {
	// Create memvalidasi saldo, menyimpan payment pending, lalu kirim ke payment.queue
	Create(ctx context.Context, userID uuid.UUID, in CreatePaymentInput) (*model.PaymentRequest, error)
	// Get mengambil status payment milik user
	Get(ctx context.Context, userID, id uuid.UUID) (*model.PaymentRequest, error)
	// ApplyStatus dipanggil status consumer saat payment-service selesai memproses
	ApplyStatus(ctx context.Context, id uuid.UUID, status model.PaymentRequestStatus, reason string) error
}

type paymentRequestService struct {
	walletRepo     repository.WalletRepository
	paymentRepo    repository.PaymentRequestRepository
	publisher      PaymentPublisher
	publishTimeout time.Duration
	now            func() time.Time
}

// ApplyStatus implements [PaymentRequestService].
func (p *paymentRequestService) ApplyStatus(ctx context.Context, id uuid.UUID, status model.PaymentRequestStatus, reason string) error {
	if !status.IsFinal() {
		return fmt.Errorf("status %q bukan status akhir", status)
	}
	updated, err := p.paymentRepo.UpdateStatus(ctx, id, status, reason)
	if err != nil {
		return fmt.Errorf("update payment status: %w", err)
	}
	if updated {
		log.Printf("payment %s -> %s %s", id, status, reason)
	} else {
		// Pesan duplikat (status sudah final) atau ID tidak dikenal: cukup diabaikan, jangan di-retry
		log.Printf("payment %s: status %s diabaikan (sudah final / tidak ditemukan)", id, status)
	}
	return nil
}

// Create implements [PaymentRequestService].
func (p *paymentRequestService) Create(ctx context.Context, userID uuid.UUID, in CreatePaymentInput) (*model.PaymentRequest, error) {
	// 1. Tolak lebih awal kalau saldo jelas tidak cukup.
	// hanya pengecekan awal: saldo bisa berubah sebelum pesan diproses
	// jadi debit di core tetap cek ulang (dengan FOR UPDATE)
	wallet, err := p.walletRepo.FindByUserID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find wallet: %w", err)
	}
	if wallet.Balance < in.Amount {
		return nil, ErrInsufficientBalance
	}

	// 2. Simpan sebagai pending. created_at dipotong ke detik supaya di JSON rapi
	// sebagai RFC3339 (2026-11-18T10:00:00Z), dan nilainya SAMA dengan yang dikirim di pesan
	pr := &model.PaymentRequest{
		ID:          uuid.New(),
		UserID:      userID,
		MerchantID:  strings.TrimSpace(in.MerchantID),
		Amount:      in.Amount,
		Description: strings.TrimSpace(in.Description),
		Status:      model.PaymentRequestPending,
		CreatedAt:   p.now().UTC().Truncate(time.Second),
	}
	if err := p.paymentRepo.Create(ctx, pr); err != nil {
		return nil, fmt.Errorf("create payment request: %w", err)
	}

	// 3. Kirim ke payment.queue
	// WithoutCancel: kalau client memutus koneksi HTTP, publish dan update status tetap diselesaikan
	bgCtx := context.WithoutCancel(ctx)
	pubCtx, cancel := context.WithTimeout(bgCtx, p.publishTimeout)
	defer cancel()

	err = p.publisher.PublishPaymentRequest(pubCtx, messaging.PaymentRequestMessage{
		PaymentID:  pr.ID,
		UserID:     pr.UserID,
		MerchantID: pr.MerchantID,
		Amount:     pr.Amount,
		CreatedAt:  pr.CreatedAt,
	})
	if err != nil {
		log.Printf("publish payment %s gagal: %v", pr.ID, err)
		if _, uerr := p.paymentRepo.UpdateStatus(bgCtx, pr.ID, model.PaymentRequestFailed, "failed to enqueue payment"); uerr != nil {
			log.Printf("tandai payment %s failed: %v", pr.ID, uerr)
		}
		return nil, ErrBrokerUnavailable
	}

	return pr, nil

}

// Get implements [PaymentRequestService].
func (p *paymentRequestService) Get(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*model.PaymentRequest, error) {
	pr, err := p.paymentRepo.FindByIDAndUser(ctx, id, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrPaymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find payment request: %w", err)
	}
	return pr, nil
}

func NewPaymentRequestService(
	walletRepo repository.WalletRepository,
	paymentRepo repository.PaymentRequestRepository,
	publisher PaymentPublisher,
	publishTimeout time.Duration,
) PaymentRequestService {
	return &paymentRequestService{
		walletRepo:     walletRepo,
		paymentRepo:    paymentRepo,
		publisher:      publisher,
		publishTimeout: publishTimeout,
		now:            time.Now,
	}
}
