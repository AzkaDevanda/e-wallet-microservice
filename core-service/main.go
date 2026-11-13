package main

import (
	"context"
	"core-service/internal/auth"
	"core-service/internal/config"
	"core-service/internal/database"
	"core-service/internal/handler"
	"core-service/internal/middleware"
	"core-service/internal/repository"
	"core-service/internal/router"
	"core-service/internal/service"
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

	userService := service.NewUserService(transactor, userRepo, walletRepo, jwtManager)
	walletService := service.NewWalletService(walletRepo)
	transactionService := service.NewTransactionService(transactor, walletRepo, transactionRepo)

	userHandler := handler.NewUserHandler(userService)
	walletHandler := handler.NewWalletHandler(walletService)
	transactionHandler := handler.NewTransactionHandler(transactionService)

	// 4. Router
	r := router.New(router.Deps{
		Health:      handler.Health(sqlDB),
		Auth:        middleware.Auth(jwtManager),
		User:        userHandler,
		Wallet:      walletHandler,
		Transaction: transactionHandler,
	})

	// 5. Jalankan server di goroutine, lalu tunggu sinyal berhenti
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

	// 6. Graceful shutdown: beri waktu request yang sedang berjalan untuk selesai
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server berhenti")
}
