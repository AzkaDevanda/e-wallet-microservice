package repository

import (
	"context"
	"errors"
	"payment-service/internal/model"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PaymentRepository interface {
	Create(ctx context.Context, payment *model.Payment) error
	FindByIDForUpdate(ctx context.Context, id, userID uuid.UUID) (*model.Payment, error)
	Save(ctx context.Context, payment *model.Payment) error
	ExpirePending(ctx context.Context, now time.Time) (int64, error)
}

type paymentRepository struct {
	db *gorm.DB
}

// Create implements [PaymentRepository].
func (r *paymentRepository) Create(ctx context.Context, payment *model.Payment) error {
	return dbFromContext(ctx, r.db).Create(payment).Error
}

// ExpirePending implements [PaymentRepository].
// ExpirePending menandai semua payment pending yang sudah lewat batas waktu sebagai expired.
// SQL: UPDATE payments SET status = 'expired', updated_at = ? WHERE status = 'pending' AND expires_at <= ?
func (r *paymentRepository) ExpirePending(ctx context.Context, now time.Time) (int64, error) {
	result := dbFromContext(ctx, r.db).
		Model(&model.Payment{}).
		Where("status = ? AND expires_at <= ?", model.PaymentPending, now).
		Updates(map[string]any{
			"status":     model.PaymentExpired,
			"updated_at": now,
		})
	return result.RowsAffected, result.Error
}

// FindByIDForUpdate implements [PaymentRepository].
// FindByIDForUpdate membaca payment milik user tertentu sekaligus mengunci barisnya.
// SQL: SELECT * FROM payments WHERE id = ? AND user_id = ? LIMIT 1 FOR UPDATE
func (r *paymentRepository) FindByIDForUpdate(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*model.Payment, error) {
	var payment model.Payment
	err := dbFromContext(ctx, r.db).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND user_id = ?", id, userID).
		First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &payment, nil
}

// Save implements [PaymentRepository].
func (r *paymentRepository) Save(ctx context.Context, payment *model.Payment) error {
	return dbFromContext(ctx, r.db).Save(payment).Error
}

func NewPaymentRepository(db *gorm.DB) PaymentRepository {
	return &paymentRepository{db: db}
}
