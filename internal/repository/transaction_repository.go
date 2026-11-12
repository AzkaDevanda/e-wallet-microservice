package repository

import (
	"context"
	"e-wallet-microservice/internal/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TransactionRepository interface {
	Create(ctx context.Context, trx *model.Transaction) error
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.Transaction, int64, error)
}

type transactionRepository struct {
	db *gorm.DB
}

func NewTransactionRepository(db *gorm.DB) TransactionRepository {
	return &transactionRepository{db: db}
}

func (r *transactionRepository) Create(ctx context.Context, trx *model.Transaction) error {
	return dbFromContext(ctx, r.db).Create(trx).Error
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
