package repository

import (
	"context"
	"errors"
	"payment-service/internal/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PaymentRepository interface {
	Create(ctx context.Context, payment *model.Payment) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.Payment, error)
	Save(ctx context.Context, payment *model.Payment) error
}

type paymentRepository struct {
	db *gorm.DB
}

// FindByID implements [PaymentRepository].
func (r *paymentRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.Payment, error) {
	var payment model.Payment
	err := dbFromContext(ctx, r.db).Where("id = ?", id).First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &payment, nil
}

// Create implements [PaymentRepository].
func (r *paymentRepository) Create(ctx context.Context, payment *model.Payment) error {
	return dbFromContext(ctx, r.db).Create(payment).Error
}

// Save implements [PaymentRepository].
func (r *paymentRepository) Save(ctx context.Context, payment *model.Payment) error {
	return dbFromContext(ctx, r.db).Save(payment).Error
}

func NewPaymentRepository(db *gorm.DB) PaymentRepository {
	return &paymentRepository{db: db}
}
