package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"payment-service/internal/client"
	"payment-service/internal/config"
	"payment-service/internal/database"
	"payment-service/internal/handler"
	"payment-service/internal/messaging"
	"payment-service/internal/repository"
	"payment-service/internal/router"
	"payment-service/internal/service"
	"syscall"
	"time"
)

func main() {
	// Mode healthcheck: dipanggil Docker lewat `/app -healthcheck` (HEALTHCHECK di Dockerfile).
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthCheck())
	}

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

	// 4. Rangkai dependency
	paymentRepo := repository.NewPaymentRepository(db)
	walletGateway := client.NewCoreWalletClient(cfg.CoreServiceURL, cfg.InternalAPIKey, cfg.CoreHTTPTimeout)
	paymentService := service.NewPaymentService(paymentRepo, walletGateway, cfg.PaymentTTL, cfg.PaymentProcessingDelay)

	// 5. Consumer payment.queue (menggantikan reconciler)
	consumer := messaging.NewConsumer(mqConn, paymentService, cfg.PaymentRetryDelay)
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := consumer.Run(ctx); err != nil {
			// Koneksi RabbitMQ putus: matikan proses.
			// Docker (restart: unless-stopped) akan menyalakannya lagi
			log.Fatalf("payment consumer berhenti: %v", err)
		}
	}()

	// 6. HTTP server (hanya health check)
	r := router.New(router.Deps{
		Health: handler.Health(sqlDB),
	})
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
	<-consumerDone // tunggu pesan yang sedang diproses selesai di-ack / dikembalikan ke queue
	log.Println("server berhenti")
}

// runHealthCheck memanggil endpoint health milik proses yang sedang berjalan di container yang sama.
// Return 0 = sehat, 1 = tidak sehat (aturan exit code HEALTHCHECK Docker).
func runHealthCheck() int {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8082"
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
