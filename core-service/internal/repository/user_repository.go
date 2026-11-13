package repository

import (
	"context"
	"core-service/internal/model"
	"errors"

	"gorm.io/gorm"
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByEmail(ctx context.Context, email string) (*model.User, error)
}

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// Create implements [UserRepository].
func (u *userRepository) Create(ctx context.Context, user *model.User) error {
	err := dbFromContext(ctx, u.db).Create(user).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicate
	}
	return err
}

// FindByEmail implements [UserRepository].
func (u *userRepository) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	err := dbFromContext(ctx, u.db).Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}
