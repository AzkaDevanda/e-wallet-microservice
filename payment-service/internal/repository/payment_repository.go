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
	// Step 14: pengganti ExpirePending. Worker tidak lagi meng-UPDATE massal,
	// tapi memeriksa payment satu per satu ke core sebelum menandai expired.
	ListOverduePendingIDs(ctx context.Context, before time.Time, limit int) ([]uuid.UUID, error)
	LockPendingByID(ctx context.Context, id uuid.UUID) (*model.Payment, error)
}

type paymentRepository struct {
	db *gorm.DB
}

// Create implements [PaymentRepository].
func (r *paymentRepository) Create(ctx context.Context, payment *model.Payment) error {
	return dbFromContext(ctx, r.db).Create(payment).Error
}

// ListOverduePendingIDs implements [PaymentRepository].
// SQL: SELECT id FROM payments WHERE status = 'pending' AND expires_at <= ? ORDER BY expires_at LIMIT ?
func (r *paymentRepository) ListOverduePendingIDs(ctx context.Context, before time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := dbFromContext(ctx, r.db).
		Model(&model.Payment{}).
		Where("status = ? AND expires_at <= ?", model.PaymentPending, before).
		Order("expires_at").
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// LockPendingByID implements [PaymentRepository].
// Mengunci satu payment pending. SKIP LOCKED: kalau baris sedang dikunci (misalnya confirm sedang berjalan),
// jangan menunggu, lewati saja; worker akan mencobanya lagi di putaran berikutnya.
// SQL: SELECT * FROM payments WHERE id = ? AND status = 'pending' LIMIT 1 FOR UPDATE SKIP LOCKED
func (r *paymentRepository) LockPendingByID(ctx context.Context, id uuid.UUID) (*model.Payment, error) {
	var payment model.Payment
	err := dbFromContext(ctx, r.db).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("id = ? AND status = ?", id, model.PaymentPending).
		First(&payment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &payment, nil
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
