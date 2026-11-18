package repository

import (
	"context"

	"gorm.io/gorm"
)

type txKey struct{}

// Transactor menjalankan beberapa operasi repository dalam satu DB transaction
type Transactor interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

type gormTransactor struct {
	db *gorm.DB
}

func NewTransactor(db *gorm.DB) Transactor {
	return &gormTransactor{db: db}
}

// WithinTransaction implements [Transactor].
// membuka Transaksi, menitipkan di konteks lalu menjalankan fn
// fn mengembalikan error -> ROLLBACK, fn mengambalikan nil -> COMMIT
func (g *gormTransactor) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// dbFromContext memakai transaksi yang sedang berjalan kalau ada
// kalau tidak ada memakai koneksi biasa
func dbFromContext(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx
	}
	return db.WithContext(ctx)
}
