package worker

import (
	"context"
	"log"
	"payment-service/internal/service"
	"time"
)

// PaymentReconciler adalah kontrak yang dibutuhkan worker (Step 14: dulu PaymentExpirer).
type PaymentReconciler interface {
	ReconcileOverdue(ctx context.Context) (service.ReconcileResult, error)
}

// RunPaymentReconciler memeriksa payment pending yang kedaluwarsa setiap interval, sampai ctx dibatalkan.
// Untuk tiap payment, core ditanya dulu: kalau saldo ternyata sudah terpotong → completed, kalau belum → expired.
func RunPaymentReconciler(ctx context.Context, reconciler PaymentReconciler, interval time.Duration) {
	log.Printf("payment reconciler berjalan setiap %s", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	run := func() {
		res, err := reconciler.ReconcileOverdue(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("payment reconciler error: %v", err)
		}
		if res.Completed+res.Expired+res.Failed > 0 {
			log.Printf("payment reconciler: completed=%d expired=%d failed=%d skipped=%d",
				res.Completed, res.Expired, res.Failed, res.Skipped)
		}
	}

	run() // jalankan sekali saat start, tanpa menunggu tick pertama
	for {
		select {
		case <-ctx.Done():
			log.Println("payment reconciler berhenti")
			return
		case <-ticker.C:
			run()
		}
	}
}
