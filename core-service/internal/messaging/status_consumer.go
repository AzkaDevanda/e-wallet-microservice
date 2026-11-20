package messaging

import (
	"context"
	"core-service/internal/model"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// StatusApplier adalah kontrak yang dibutuhkan consumer (dipenuhi oleh service.PaymentRequestService)
// Interface didefinisikan di sini supaya paket messaging tidak meng-import paket service (mencegah import cycle)
type StatusApplier interface {
	ApplyStatus(ctx context.Context, id uuid.UUID, status model.PaymentRequestStatus, reason string) error
}

// StatusConsumer mendengarkan status.queue lalu meng-update status payment di core_db
type StatusConsumer struct {
	conn       *amqp.Connection
	applier    StatusApplier
	retryDelay time.Duration
}

func NewStatusConsumer(conn *amqp.Connection, applier StatusApplier, retryDelay time.Duration) *StatusConsumer {
	return &StatusConsumer{conn: conn, applier: applier, retryDelay: retryDelay}
}

// Run memproses pesan sampai ctx dibatalkan (nil) atau koneksi RabbitMQ putus (error)
func (c *StatusConsumer) Run(ctx context.Context) error {
	ch, err := c.conn.Channel() // channel SENDIRI, terpisah dari publisher
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("set qos: %w", err)
	}

	deliveries, err := ch.Consume(
		QueueStatus,    // queue
		"core-service", // consumer tag
		false,          // auto-ack: FALSE, ack manual setelah DB ter-update
		false,          // exclusive
		false,          // no-local
		false,          //no-wait
		nil,            // argumen
	)
	if err != nil {
		return fmt.Errorf("consume %s: %w", QueueStatus, err)
	}
	log.Printf("status consumer mendengarkan %s", QueueStatus)

	for {
		select {
		case <-ctx.Done():
			log.Println("status consumer berhenti")
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("channel status consumer tertutup (koneksi rabbitmq putus?)")
			}
			c.handle(ctx, d)
		}
	}

}

func (c *StatusConsumer) handle(ctx context.Context, d amqp.Delivery) {
	var msg PaymentStatusMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil || msg.PaymentID == uuid.Nil {
		log.Printf("pesan status rusak, dibuang: %s", d.Body)
		_ = d.Nack(false, false) // requeue=false: dicoba ulang pun tetap rusak
		return
	}

	status := model.PaymentRequestStatus(msg.Status)
	if !status.IsFinal() {
		log.Printf("status %q untuk payment %s tidak dikenal, dibuang", msg.Status, msg.PaymentID)
		_ = d.Nack(false, false)
		return
	}

	if err := c.applier.ApplyStatus(ctx, msg.PaymentID, status, msg.Reason); err != nil {
		log.Printf("update status payment %s gagal: %v (dicoba lagi dalam %s)", msg.PaymentID, err, c.retryDelay)
		wait(ctx, c.retryDelay) // jeda dulu, supaya tidak berputar ratusan kali per detik saat DB mati
		_ = d.Nack(false, true) // requeue=true: masuk antrian lagi
		return
	}

	if err := d.Ack(false); err != nil {
		log.Printf("ack status payment %s gagal: %v", msg.PaymentID, err)
	}
}

// wait menunggu selama d, atau berhenti lebih cepat kalau ctx dibatalkan.
func wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
