package service

import (
	"context"
	"core-service/internal/model"
	"core-service/internal/repository"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrSelfTransfer        = errors.New("cannot transfer to your own wallet")
	ErrRecipientNotFound   = errors.New("recipient not found")
)

// TopUpResult adalah hasil top-up: catatan transaksi dan saldo setelah top-up.
type TopUpResult struct {
	Transaction *model.Transaction
	NewBalance  int64
}

// HistoryResult adalah satu halaman riwayat transaksi beserta info pagination.
type HistoryResult struct {
	Items      []model.Transaction
	Page       int
	TotalPages int
	TotalItems int64
}

type TransactionService interface {
	TopUp(ctx context.Context, userID uuid.UUID, amount int64) (*TopUpResult, error)
	Transfer(ctx context.Context, fromUserID, toUserID uuid.UUID, amount int64) (*model.Transaction, error)
	History(ctx context.Context, userID uuid.UUID, page, limit int) (*HistoryResult, error)
}

type transactionService struct {
	transactor      repository.Transactor
	walletRepo      repository.WalletRepository
	transactionRepo repository.TransactionRepository
}

func NewTransactionService(
	transactor repository.Transactor,
	walletRepo repository.WalletRepository,
	transactionRepo repository.TransactionRepository,
) TransactionService {
	return &transactionService{
		transactor:      transactor,
		walletRepo:      walletRepo,
		transactionRepo: transactionRepo,
	}
}

// History implements [TransactionService].
func (s *transactionService) History(ctx context.Context, userID uuid.UUID, page int, limit int) (*HistoryResult, error) {
	offset := (page - 1) * limit

	items, total, err := s.transactionRepo.ListByUserID(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}

	totalPages := int((total + int64(limit) - 1) / int64(limit))

	return &HistoryResult{
		Items:      items,
		Page:       page,
		TotalPages: totalPages,
		TotalItems: total,
	}, nil
}

// Transfer implements [TransactionService].
func (s *transactionService) Transfer(ctx context.Context, fromUserID uuid.UUID, toUserID uuid.UUID, amount int64) (*model.Transaction, error) {
	if fromUserID == toUserID {
		return nil, ErrSelfTransfer
	}

	var outTrx *model.Transaction

	err := s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		// 1. Kunci kedua wallet dalam urutan yang SELALU sama (user ID terkecil dulu),
		//    supaya dua transfer berlawanan arah tidak saling menunggu (deadlock).
		wallets, err := s.lockWallets(ctx, fromUserID, toUserID)
		if err != nil {
			return err
		}
		sender, receiver := wallets[fromUserID], wallets[toUserID]

		// 2. Cek saldo. Aman, karena baris sender sudah dikunci: tidak ada request lain
		//    yang bisa mengubah saldonya sampai transaksi ini selesai.
		if sender.Balance < amount {
			return ErrInsufficientBalance
		}

		// 3. Pindahkan saldo.
		if _, err := s.walletRepo.AddBalance(ctx, sender.UserID, -amount); err != nil {
			return err
		}
		if _, err := s.walletRepo.AddBalance(ctx, receiver.UserID, amount); err != nil {
			return err
		}

		// 4. Catat di ledger: satu baris untuk masing-masing pihak, dihubungkan reference yang sama.
		reference := uuid.New()
		out := &model.Transaction{
			UserID:      sender.UserID,
			Type:        model.TransactionTransferOut,
			Amount:      amount,
			From:        sender.UserID.String(),
			To:          receiver.UserID.String(),
			Status:      model.TransactionCompleted,
			ReferenceID: &reference,
		}
		in := &model.Transaction{
			UserID:      receiver.UserID,
			Type:        model.TransactionTransferIn,
			Amount:      amount,
			From:        sender.UserID.String(),
			To:          receiver.UserID.String(),
			Status:      model.TransactionCompleted,
			ReferenceID: &reference,
		}
		if err := s.transactionRepo.Create(ctx, out); err != nil {
			return err
		}
		if err := s.transactionRepo.Create(ctx, in); err != nil {
			return err
		}

		outTrx = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return outTrx, nil
}

// lockWallets mengunci wallet pengirim dan penerima dengan urutan user ID yang konsisten.
func (s *transactionService) lockWallets(ctx context.Context, fromUserID, toUserID uuid.UUID) (map[uuid.UUID]*model.Wallet, error) {
	first, second := fromUserID, toUserID
	if second.String() < first.String() {
		first, second = second, first
	}

	wallets := make(map[uuid.UUID]*model.Wallet, 2)
	for _, id := range []uuid.UUID{first, second} {
		wallet, err := s.walletRepo.FindByUserIDForUpdate(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			if id == fromUserID {
				return nil, ErrWalletNotFound
			}
			return nil, ErrRecipientNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("lock wallet: %w", err)
		}
		wallets[id] = wallet
	}
	return wallets, nil
}

// TopUp implements [TransactionService].
func (s *transactionService) TopUp(ctx context.Context, userID uuid.UUID, amount int64) (*TopUpResult, error) {
	var result TopUpResult

	// Tambah saldo dan catat transaksi dalam SATU DB transaction.
	err := s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		wallet, err := s.walletRepo.AddBalance(ctx, userID, amount)
		if err != nil {
			return err
		}

		trx := &model.Transaction{
			UserID: userID,
			Type:   model.TransactionTopUp,
			Amount: amount,
			To:     userID.String(),
			Status: model.TransactionCompleted,
		}
		if err := s.transactionRepo.Create(ctx, trx); err != nil {
			return err
		}

		result = TopUpResult{Transaction: trx, NewBalance: wallet.Balance}
		return nil
	})

	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrWalletNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("top up: %w", err)
	}

	return &result, nil
}
