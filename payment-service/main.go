package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"payment-service/internal/auth"
	"payment-service/internal/client"
	"payment-service/internal/config"
	"payment-service/internal/database"
	"payment-service/internal/handler"
	"payment-service/internal/middleware"
	"payment-service/internal/repository"
	"payment-service/internal/router"
	"payment-service/internal/service"
	"payment-service/internal/worker"
	"syscall"
	"time"
)

func main() {
	// ctx dibatalkan saat menerima Ctrl+C (SIGINT) atau SIGTERM (misalnya dari Docker).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Konfigurasi
	cfg := config.LoadConfig()

	// 2. Database (payment_db)
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

	// 3. Rangkai dependency
	jwtParser := auth.NewJWTParser(cfg.JWTSecret)

	transactor := repository.NewTransactor(db)
	paymentRepo := repository.NewPaymentRepository(db)

	walletGateway := client.NewCoreWalletClient(cfg.CoreServiceURL, cfg.InternalAPIKey, cfg.CoreHTTPTimeout)
	paymentService := service.NewPaymentService(transactor, paymentRepo, walletGateway, cfg.PaymentTTL)

	paymentHandler := handler.NewPaymentHandler(paymentService)

	// 4. Router
	r := router.New(router.Deps{
		Health:  handler.Health(sqlDB),
		Auth:    middleware.Auth(jwtParser),
		Payment: paymentHandler,
	})

	// 5. Background worker untuk payment yang kedaluwarsa
	go worker.RunPaymentExpiry(ctx, paymentService, cfg.PaymentExpiryInterval)

	// 6. Jalankan server di goroutine, lalu tunggu sinyal berhenti
	srv := &http.Server{
		Addr:    ":" + cfg.AppPort,
		Handler: r,
	}
	go func() {
		log.Printf("payment-service berjalan di port %s (core-service: %s)", cfg.AppPort, cfg.CoreServiceURL)
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
