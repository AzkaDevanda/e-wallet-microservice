package repository

import (
	"context"
	"core-service/internal/model"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PaymentRequestRepository interface {
	Create(ctx context.Context, pr *model.PaymentRequest) error
	FindByIDAndUser(ctx context.Context, id, userID uuid.UUID) (*model.PaymentRequest, error)
	// UpdateStatus hanya mengubah payment yang belum final.
	// Mengembalikan false kalau tidak ada baris yang berubah
	UpdateStatus(ctx context.Context, id uuid.UUID, status model.PaymentRequestStatus, reason string) (bool, error)
}

type paymentRequestRepository struct {
	db *gorm.DB
}

// Create implements [PaymentRequestRepository].
func (p *paymentRequestRepository) Create(ctx context.Context, pr *model.PaymentRequest) error {
	return dbFromContext(ctx, p.db).Create(pr).Error
}

// FindByIDAndUser implements [PaymentRequestRepository].
// Payment milik user lain dianggap tidak ada (404), supaya tidak bocor
// SQL : SELECT * FROM payment_requests WHERE id = ? AND user_id = ? LIMIT 1
func (p *paymentRequestRepository) FindByIDAndUser(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*model.PaymentRequest, error) {
	var pr model.PaymentRequest

	err := dbFromContext(ctx, p.db).Where("id = ? AND user_id = ?", id, userID).First(&pr).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &pr, nil
}

// UpdateStatus implements [PaymentRequestRepository].
func (p *paymentRequestRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status model.PaymentRequestStatus, reason string) (bool, error) {
	result := dbFromContext(ctx, p.db).
		Model(&model.PaymentRequest{}).
		Where("id = ? AND status in ?", id,
			[]model.PaymentRequestStatus{
				model.PaymentRequestPending,
				model.PaymentRequestFailed}).
		Updates(map[string]any{"status": status, "reason": reason})

	if result.Error != nil {
		return false, result.Error
	}

	return result.RowsAffected > 0, nil
}

func NewPaymentRequestRepository(db *gorm.DB) PaymentRequestRepository {
	return &paymentRequestRepository{db: db}
}
