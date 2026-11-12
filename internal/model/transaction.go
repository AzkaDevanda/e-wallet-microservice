package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TransactionType string

const (
	TransactionTopUp       TransactionType = "topup"
	TransactionTransferIn  TransactionType = "transfer_in"
	TransactionTransferOut TransactionType = "transfer_out"
	TransactionPayment     TransactionType = "payment"
)

type TransactionStatus string

const (
	TransactionCompleted TransactionStatus = "completed"
	TransactionPending   TransactionStatus = "pending"
	TransactionFailed    TransactionStatus = "failed"
)

type Transaction struct {
	ID          uuid.UUID         `gorm:"type:uuid;primaryKey"`
	UserID      uuid.UUID         `gorm:"type:uuid;not null;index:idx_transactions_user_created,priority:1"`
	Type        TransactionType   `gorm:"type:varchar(20);not null"`
	Amount      int64             `gorm:"not null;check:chk_transaction_amount_positive,amount>0"`
	From        string            `gorm:"column:from_account;type:varchar(100)"`
	To          string            `gorm:"column:to_account;type:varchar(100)"`
	Status      TransactionStatus `gorm:"type:varchar(20);not null"`
	ReferenceID *uuid.UUID        `gorm:"type:uuid;index"`
	CreatedAt   time.Time         `gorm:"index:idx_transactions_user_created, priority:2"`
}

func (t *Transaction) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}
