package repository

import (
	"context"
	"core-service/internal/model"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TransactionRepository interface {
	Create(ctx context.Context, trx *model.Transaction) error
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.Transaction, int64, error)
	FindByReference(ctx context.Context, referenceID uuid.UUID, txType model.TransactionType) (*model.Transaction, error)
}

type transactionRepository struct {
	db *gorm.DB
}

func NewTransactionRepository(db *gorm.DB) TransactionRepository {
	return &transactionRepository{db: db}
}

// Create menyimpan satu baris ledger
// Kalau melanggar unique index (reference_id, type), errornya diterjemahkan jadi ErrDuplicate
func (r *transactionRepository) Create(ctx context.Context, trx *model.Transaction) error {
	err := dbFromContext(ctx, r.db).Create(trx).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	return err
}

// ListByUserID mengambil satu halaman transaksi milik user (terbaru dulu)
// beserta jumlah total transaksinya.
func (r *transactionRepository) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.Transaction, int64, error) {
	db := dbFromContext(ctx, r.db)

	var total int64
	if err := db.Model(&model.Transaction{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.Transaction
	err := db.Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&items).Error
	if err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// FindByReference implements [TransactionRepository].
// FindByReference mencari baris
func (r *transactionRepository) FindByReference(ctx context.Context, referenceID uuid.UUID, txType model.TransactionType) (*model.Transaction, error) {
	var trx model.Transaction
	err := dbFromContext(ctx, r.db).
		Where("reference_id = ? AND type = ?", referenceID, txType).
		First(&trx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &trx, nil
}
