package repository

import (
	"context"
	"core-service/internal/model"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WalletRepository interface {
	Create(ctx context.Context, wallet *model.Wallet) error
	FindByUserID(ctx context.Context, userID uuid.UUID) (*model.Wallet, error)
	FindByUserIDForUpdate(ctx context.Context, userID uuid.UUID) (*model.Wallet, error)
	AddBalance(ctx context.Context, userID uuid.UUID, amount int64) (*model.Wallet, error)
}

type walletRepository struct {
	db *gorm.DB
}

func NewWalletRepository(db *gorm.DB) WalletRepository {
	return &walletRepository{db: db}
}

func (r *walletRepository) Create(ctx context.Context, wallet *model.Wallet) error {
	return dbFromContext(ctx, r.db).Create(wallet).Error
}

// FindByUserID implements [WalletRepository].
func (r *walletRepository) FindByUserID(ctx context.Context, userID uuid.UUID) (*model.Wallet, error) {
	return r.findByUserID(dbFromContext(ctx, r.db), userID)
}

// FindByUserIDForUpdate implements [WalletRepository].
// FindByUserIDForUpdate membaca wallet sekaligus mengunci barisnya sampai transaksi selesai.
// SQL: SELECT * FROM wallets WHERE user_id = ? LIMIT 1 FOR UPDATE
// Hanya bermakna kalau dipanggil di dalam Transactor.WithinTransaction.
func (r *walletRepository) FindByUserIDForUpdate(ctx context.Context, userID uuid.UUID) (*model.Wallet, error) {
	db := dbFromContext(ctx, r.db).Clauses(clause.Locking{Strength: "UPDATE"})
	return r.findByUserID(db, userID)
}

func (r *walletRepository) findByUserID(db *gorm.DB, userID uuid.UUID) (*model.Wallet, error) {
	var wallet model.Wallet
	err := db.Where("user_id = ?", userID).First(&wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

// AddBalance implements [WalletRepository].
// AddBalance menambah (atau mengurangi, kalau amount negatif) saldo secara atomik,
// lalu mengembalikan wallet terbaru.
// SQL: UPDATE wallets SET balance = balance + ?, updated_at = ? WHERE user_id = ? RETURNING *
func (r *walletRepository) AddBalance(ctx context.Context, userID uuid.UUID, amount int64) (*model.Wallet, error) {
	var wallet model.Wallet
	result := dbFromContext(ctx, r.db).
		Model(&wallet).
		Clauses(clause.Returning{}).
		Where("user_id = ?", userID).
		Update("balance", gorm.Expr("balance + ?", amount))
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &wallet, nil
}
