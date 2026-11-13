package service

import (
	"context"
	"e-wallet-microservice/internal/dto"
	"e-wallet-microservice/internal/model"
	"e-wallet-microservice/internal/repository"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrEmailAlreadyExists = errors.New("user with this email already exists")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

type TokenGenerator interface {
	Generate(userID uuid.UUID) (string, error)
}

type UserService interface {
	Register(ctx context.Context, req dto.RegisterRequest) (*model.User, error)
	Login(ctx context.Context, req dto.LoginRequest) (string, error)
}

type userService struct {
	transactor repository.Transactor
	userRepo   repository.UserRepository
	walletRepo repository.WalletRepository
	tokens     TokenGenerator
}

func NewUserService(
	transactor repository.Transactor,
	userRepo repository.UserRepository,
	walletRepo repository.WalletRepository,
	tokens TokenGenerator,
) UserService {
	return &userService{
		transactor: transactor,
		userRepo:   userRepo,
		walletRepo: walletRepo,
		tokens:     tokens,
	}
}

// Login implements [UserService].
func (s *userService) Login(ctx context.Context, req dto.LoginRequest) (string, error) {
	user, err := s.userRepo.FindByEmail(ctx, normalizeEmail(req.Email))
	if errors.Is(err, repository.ErrNotFound) {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("find user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return "", ErrInvalidCredentials
	}

	token, err := s.tokens.Generate(user.ID)
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return token, nil
}

// Register implements [UserService].
func (s *userService) Register(ctx context.Context, req dto.RegisterRequest) (*model.User, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &model.User{
		Name:     strings.TrimSpace(req.Name),
		Email:    normalizeEmail(req.Email),
		Password: string(hashed),
	}

	// User dan wallet dibuat dalam SATU transaksi: dua-duanya berhasil, atau dua-duanya batal.
	err = s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := s.userRepo.Create(ctx, user); err != nil {
			return err
		}
		return s.walletRepo.Create(ctx, &model.Wallet{UserID: user.ID})
	})
	if errors.Is(err, repository.ErrDuplicate) {
		return nil, ErrEmailAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("register user: %w", err)
	}

	return user, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
