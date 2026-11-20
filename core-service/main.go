package main

import (
	"context"
	"core-service/internal/auth"
	"core-service/internal/config"
	"core-service/internal/database"
	"core-service/internal/handler"
	"core-service/internal/messaging"
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
	// Mode healthcheck: dipanggil Docker lewat `/app -healthcheck` (HEALTHCHECK di Dockerfile).
	// Harus dicek paling awal, supaya tidak ikut membaca config, konek DB, atau menyalakan server.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthCheck())
	}

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

	// 3. RabbitMQ (Part 3)
	mqConn, err := messaging.Connect(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("gagal terhubung ke rabbitmq: %v", err)
	}
	defer mqConn.Close()

	if err := messaging.SetupTopology(mqConn); err != nil {
		log.Fatalf("gagal menyiapkan topologi rabbitmq: %v", err)
	}
	log.Println("rabbitmq terhubung, exchange dan queue siap")

	publisher, err := messaging.NewPublisher(mqConn)
	if err != nil {
		log.Fatalf("gagal membuat publisher: %v", err)
	}
	defer publisher.Close()

	// 4. Rangkai dependency dari dalam ke luar: repository -> service -> handler
	jwtManager := auth.NewJWTManager(cfg.JWTSecret, cfg.TokenTTL)

	transactor := repository.NewTransactor(db)
	userRepo := repository.NewUserRepository(db)
	walletRepo := repository.NewWalletRepository(db)
	transactionRepo := repository.NewTransactionRepository(db)
	paymentRequestRepo := repository.NewPaymentRequestRepository(db)

	userService := service.NewUserService(transactor, userRepo, walletRepo, jwtManager)
	walletService := service.NewWalletService(walletRepo)
	transactionService := service.NewTransactionService(transactor, walletRepo, transactionRepo)
	walletDebitService := service.NewWalletDebitService(transactor, walletRepo, transactionRepo)
	paymentRequestService := service.NewPaymentRequestService(walletRepo, paymentRequestRepo, publisher, 5*time.Second)

	userHandler := handler.NewUserHandler(userService)
	walletHandler := handler.NewWalletHandler(walletService)
	transactionHandler := handler.NewTransactionHandler(transactionService)
	paymentHandler := handler.NewPaymentHandler(paymentRequestService)
	internalWalletHandler := handler.NewInternalWalletHandler(walletService, walletDebitService)

	// 5. Router
	r := router.New(router.Deps{
		Health:       handler.Health(sqlDB),
		Auth:         middleware.Auth(jwtManager),
		InternalAuth: middleware.InternalAuth(cfg.InternalAPIKey),
		User:         userHandler,
		Wallet:       walletHandler,
		Transaction:  transactionHandler,
		Payment:      paymentHandler,
		Internal:     internalWalletHandler,
	})

	// 6. Consumer status.queue (extra credit): update status payment
	// dari hasil payment-service
	statusConsumer := messaging.NewStatusConsumer(mqConn, paymentRequestService, 5*time.Second)
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := statusConsumer.Run(ctx); err != nil {
			// Koneksi RabbitMQ putus: matikan proses. Docker (restart: unless-stopped) akan menyalakannya lagi
			log.Fatalf("status consumer berhenti: %v", err)
		}
	}()

	// 7. Jalankan server di goroutine, lalu tunggu sinyal berhenti
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

	// 8. Graceful shutdown: beri waktu request yang sedang berjalan untuk selesai
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	<-consumerDone // tunggu pesan status yang sedang diproses selesai di-ack
	log.Println("server berhenti")
}

// runHealthCheck memanggil endpoint health milik proses yang sedang berjalan di container yang sama.
// Return 0 = sehat, 1 = tidak sehat (aturan exit code HEALTHCHECK Docker).
func runHealthCheck() int {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8081"
	}

	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/v1/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
