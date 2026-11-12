package main

import (
	"context"
	"e-wallet-microservice/internal/auth"
	"e-wallet-microservice/internal/config"
	"e-wallet-microservice/internal/database"
	"e-wallet-microservice/internal/handler"
	"e-wallet-microservice/internal/middleware"
	"e-wallet-microservice/internal/repository"
	"e-wallet-microservice/internal/router"
	"e-wallet-microservice/internal/service"
	"e-wallet-microservice/internal/worker"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// ctx dibatalkan saat menerima Ctrl+C (SIGINT) atau SIGTERM (misalnya dari Docker).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Konfigurasi
	cfg := config.LoadConfig()

	// 2. Database
	db, err := database.NewPostgres(cfg)
	if err != nil {
		log.Fatalf("gagal terhubung ke database: %v", err)
	}
	log.Println("database terhubung")

	if err := database.Migrate(db); err != nil {
		log.Fatalf("migrasi gagal: %v", err)
	}
	log.Println("migrasi selesai")

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("gagal mengambil sql.DB: %v", err)
	}
	defer sqlDB.Close()

	// 3. Rangkai dependency dari dalam ke luar: repository -> service -> handler
	jwtManager := auth.NewJWTManager(cfg.JWTSecret, cfg.TokenTTL)

	transactor := repository.NewTransactor(db)
	userRepo := repository.NewUserRepository(db)
	walletRepo := repository.NewWalletRepository(db)
	transactionRepo := repository.NewTransactionRepository(db)
	paymentRepo := repository.NewPaymentRepository(db)

	userService := service.NewUserService(transactor, userRepo, walletRepo, jwtManager)
	walletService := service.NewWalletService(walletRepo)
	transactionService := service.NewTransactionService(transactor, walletRepo, transactionRepo)

	walletGateway := service.NewLocalWalletGateway(walletRepo, transactionRepo)
	paymentService := service.NewPaymentService(transactor, paymentRepo, walletGateway, cfg.PaymentTTL)

	userHandler := handler.NewUserHandler(userService)
	walletHandler := handler.NewWalletHandler(walletService)
	transactionHandler := handler.NewTransactionHandler(transactionService)
	paymentHandler := handler.NewPaymentHandler(paymentService)

	// 4. Router
	r := router.New(router.Deps{
		Health:      handler.Health(sqlDB),
		Auth:        middleware.Auth(jwtManager),
		User:        userHandler,
		Wallet:      walletHandler,
		Transaction: transactionHandler,
		Payment:     paymentHandler,
	})

	// 5. Background worker untuk payment yang kedaluwarsa
	go worker.RunPaymentExpiry(ctx, paymentService, cfg.PaymentExpiryInterval)

	// 6. Jalankan server di goroutine, lalu tunggu sinyal berhenti
	srv := &http.Server{
		Addr:    ":" + cfg.AppPort,
		Handler: r,
	}
	go func() {
		log.Printf("server berjalan di port %s", cfg.AppPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server gagal berjalan: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("menerima sinyal berhenti, mematikan server...")

	// 7. Graceful shutdown: beri waktu request yang sedang berjalan untuk selesai
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server berhenti")
}
