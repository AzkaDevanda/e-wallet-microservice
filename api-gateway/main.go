package main

import (
	"api-gateway/internal/config"
	"api-gateway/internal/proxy"
	"api-gateway/internal/router"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.LoadConfig()

	handler := router.New(router.Upstreams{
		Core: proxy.New("core-service", cfg.CoreServiceURL, cfg.UpstreamTimeout),
	})

	srv := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      cfg.UpstreamTimeout + 5*time.Second, // > waktu tunggu upstream
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("api-gateway berjalan di port %s (core: %s)", cfg.AppPort, cfg.CoreServiceURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server gagal berjalan: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("menerima sinyal berhenti, mematikan gateway...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("gateway berhenti")
}
