package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"payment-service/internal/service"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// PaymentProcessor adalah kontrak yang dibutuhkan consumer (dipenuhi oleh service.PaymentService)
type PaymentProcessor interface {
	Process(ctx context.Context, in service.ProcessInput) (*service.ProcessResult, error)
}

// Consumer mendengarkan payment.queue, memproses pembayaran,
// lalu mengirim hasilnya ke status.queue
type Consumer struct {
	conn       *amqp.Connection
	processor  PaymentProcessor
	retryDelay time.Duration
}

func NewConsumer(conn *amqp.Connection, processor PaymentProcessor, retryDelay time.Duration) *Consumer {
	return &Consumer{conn: conn, processor: processor, retryDelay: retryDelay}
}

// Run memproses pesan sampai ctx dibatalkan (nil) atau koneksi RabbitMQ putus (error)
func (c *Consumer) Run(ctx context.Context) error {
	// Channel 1: untuk MENERIMA pesan dari payment.queue
	consumeCh, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("open consume channel: %w", err)
	}
	defer consumeCh.Close()

	// Fair dispatch: RabbitMQ hanya kasih 1 pesan yang belum di-ack,
	// Kalau payment-service dijalankan beberapa instance, pesan dibagi ke instance yang sedang kosong
	if err := consumeCh.Qos(1, 0, false); err != nil {
		return fmt.Errorf("set qos: %w", err)
	}

	// Channel 2: untuk MENGIRIM hasil ke status.queue (confirm mode).
	// Jangan dicampur dengan channel consume
	publishCh, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("open publish channel: %w", err)
	}
	defer publishCh.Close()
	if err := publishCh.Confirm(false); err != nil {
		return fmt.Errorf("enable confirm mode: %w", err)
	}

	deliveries, err := consumeCh.Consume(
		QueuePayment,      // queue
		"payment-service", // consumer tag
		false,             // auto-ack: FALSE, ack manual setelah selesai diproses
		false,             // exclusive
		false,             // no-local
		false,             // no-wait
		nil,               // argumen
	)
	if err != nil {
		return fmt.Errorf("consume %s: %w", QueuePayment, err)
	}
	log.Printf("payment consumer mendengarkan %s", QueuePayment)

	for {
		select {
		case <-ctx.Done():
			log.Println("payment consumer berhenti")
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("channel consumer tertutup (koneksi rabbitmq putus?)")
			}
			c.handle(ctx, publishCh, d)
		}
	}
}

// handle memproses SATU pesan. Urutannya wajib: proses + simpan DB -> kirim status -> ack
// Kalau proses mati tengah jalan, pesan belum di-ack sehingga RabbitMQ mengirimnya lagi
// dan Process aman dipanggil ulang (idempoten)
func (c *Consumer) handle(ctx context.Context, publishCh *amqp.Channel, d amqp.Delivery) {
	var msg PaymentRequestMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		log.Printf("pesan rusak dibuang: %v (%s)", err, d.Body)
		_ = d.Nack(false, false) // requeue=false: dicoba ulang pun tetap rusak
		return
	}

	res, err := c.processor.Process(ctx, service.ProcessInput{
		PaymentID:  msg.PaymentID,
		UserID:     msg.UserID,
		MerchantID: msg.MerchantID,
		Amount:     msg.Amount,
		CreatedAt:  msg.CreatedAt,
	})
	switch {
	case errors.Is(err, service.ErrInvalidMessage):
		log.Printf("payment %s ditolak: %v", msg.PaymentID, err)
		_ = d.Nack(false, false)
		return
	case err != nil:
		// Gagal sementara (core mati, DB error, atau service sedang dimatikan): masukkan lagi ke antrian
		// Retry tidak berjalan selamanya: setelah 10 menit, Process akan menandainya expired
		if ctx.Err() == nil {
			log.Printf("payment %s gagal diproses: %v (dicoba lagi dalam %s)", msg.PaymentID, err, c.retryDelay)
			wait(ctx, c.retryDelay)
		}
		_ = d.Nack(false, true)
		return
	}

	// Kirim hasil ke status.queue. WithoutCancel: tetap dikirim walaupun service sedang dimatikan
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	status := PaymentStatusMessage{
		PaymentID:   res.PaymentID,
		Status:      string(res.Status),
		Reason:      res.Reason,
		ProcessedAt: res.ProcessedAt,
	}
	if err := publishJSON(pubCtx, publishCh, RoutingKeyStatus,
		res.PaymentID.String(), status); err != nil {
		// Hasil sudah tersimpan di payment_db.
		// Saat pesan dikirim ulang,
		// Process langsung mengembalikan hasil yang sama (tanpa debit lagi),
		// lalu status dicoba dikirim lagi
		log.Printf("kirim status payment %s gagal: %v (pesan dikembalikan ke antrian)", res.PaymentID, err)
		_ = d.Nack(false, true)
		return
	}

	if err := d.Ack(false); err != nil {
		log.Printf("ack payment %s gagal: %v", res.PaymentID, err)
		return
	}
	log.Printf("payment %s selesai: %s %s", res.PaymentID, res.Status, res.Reason)
}

// wait menunggu selama d, atau berhenti lebih cepat kalau ctx dibatalkan
func wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
