package worker

import (
	"context"
	"log"
	"time"
)

// PaymentExpirer adalah kontrak yang dibutuhkan worker.
type PaymentExpirer interface {
	ExpireOverdue(ctx context.Context) (int64, error)
}

// RunPaymentExpiry menandai payment yang kedaluwarsa setiap interval, sampai ctx dibatalkan.
func RunPaymentExpiry(ctx context.Context, expirer PaymentExpirer, interval time.Duration) {
	log.Printf("payment expiry worker berjalan setiap %s", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	run := func() {
		n, err := expirer.ExpireOverdue(ctx)
		if err != nil {
			log.Printf("payment expiry worker error: %v", err)
			return
		}
		if n > 0 {
			log.Printf("payment expiry worker: %d payment ditandai expired", n)
		}
	}

	run() // jalankan sekali saat start, tanpa menunggu tick pertama
	for {
		select {
		case <-ctx.Done():
			log.Println("payment expiry worker berhenti")
			return
		case <-ticker.C:
			run()
		}
	}
}
