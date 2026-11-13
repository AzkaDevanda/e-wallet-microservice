package service

import (
	"context"
	"core-service/internal/model"
	"core-service/internal/repository"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrIdempotencyConflict: referenceID yang sama dipakai lagi dengan data yang berbeda
// (user, amount, atau merchant lain). ini bukan entry tapi kesalahan pemanggil
var ErrIdempotencyConflict = errors.New("referenceID already used with different data")

// DebitInput adalah permintaan potong saldo dari payment-service
type DebitInput struct {
	UserID      uuid.UUID
	Amount      int64
	ReferenceID uuid.UUID // ID Payment : kunci idempotency
	MerchantID  string
}

// DebitResult adalah hasil debit, Replayed = true kalau referenceID ini sudah pernah diproses
// dan saldo tidak dipotong lagi
type DebitResult struct {
	Transction *model.Transaction
	NewBalance int64
	Replayed   bool
}

// WalletDebitService memotong saldo untuk pembayaran secara indempotent
// Dipanggil oleh endpoint internal, bukan oleh user
type WalletDebitService interface {
	Debit(ctx context.Context, in DebitInput) (*DebitResult, error)
}

type walletDebitService struct {
	transactor      repository.Transactor
	walletRepo      repository.WalletRepository
	transactionRepo repository.TransactionRepository
}

// Debit implements [WalletDebitService].
func (w *walletDebitService) Debit(ctx context.Context, in DebitInput) (*DebitResult, error) {
	var result *DebitResult

	err := w.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		// 1. Kunci wallet dulu, Request lain untuk wallet yang sama (termasuk retry
		// dengan referanceID yang sama) menunggu di sini sampai transaksi ini selesai.
		wallet, err := w.walletRepo.FindByUserIDForUpdate(ctx, in.UserID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrWalletNotFound
		}
		if err != nil {
			return fmt.Errorf("lock wallet: %w", err)
		}

		// 2. Baru cek: apakah referenceID ini sudah pernah memotong saldo?
		existing, err := w.transactionRepo.FindByReference(ctx, in.ReferenceID, model.TransactionPayment)
		if err == nil {
			// Sudah pernah, pastikan ini benar benar retry dari permintaan yang sama
			if existing.UserID != in.UserID || existing.Amount != in.Amount || existing.To != in.MerchantID {
				return ErrIdempotencyConflict
			}
			result = &DebitResult{
				Transction: existing,
				NewBalance: wallet.Balance,
				Replayed:   true,
			}
			return nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("find by reference: %w", err)
		}

		// 3. Belum pernah: proses debit seperti biasa
		if wallet.Balance < in.Amount {
			return ErrInsufficientBalance
		}

		update, err := w.walletRepo.AddBalance(ctx, in.UserID, -in.Amount)
		if err != nil {
			return fmt.Errorf("debit wallet: %w", err)
		}

		trx := &model.Transaction{
			UserID:      in.UserID,
			Type:        model.TransactionPayment,
			Amount:      in.Amount,
			From:        in.UserID.String(),
			To:          in.MerchantID,
			Status:      model.TransactionCompleted,
			ReferenceID: &in.ReferenceID,
		}

		if err := w.transactionRepo.Create(ctx, trx); err != nil {
			// Jaringan pengaman: unique index (reference_id, type) menolak baris kedua
			// Seluruh transaksi (termasuk potongan saldo di atas) ikut ROLLBACK
			if errors.Is(err, repository.ErrDuplicate) {
				return ErrIdempotencyConflict
			}
			return fmt.Errorf("record payment transaction: %w", err)
		}

		result = &DebitResult{Transction: trx, NewBalance: update.Balance}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil

}

func NewWalletDebitService(
	transactor repository.Transactor,
	walletRepo repository.WalletRepository,
	transactionRepo repository.TransactionRepository,
) WalletDebitService {
	return &walletDebitService{
		transactor:      transactor,
		walletRepo:      walletRepo,
		transactionRepo: transactionRepo,
	}
}
