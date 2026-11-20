package messaging

import (
	"context"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher mengirim pesan permintaan pembayaran ke payment.queue
// Satu channel khusus untuk publish (jangan dipakai bersama consumer)
type Publisher struct {
	mu sync.Mutex // channel AMQP tidak aman dipakai banyak goroutine sekaligus
	ch *amqp.Channel
}

func NewPublisher(conn *amqp.Connection) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open channel: %w", err)
	}

	// Confirm mode: broker membalas ack/nack untuk setiap pesan yang kita kirim
	if err := ch.Confirm(false); err != nil {
		ch.Close()
		return nil, fmt.Errorf("enable confirm mode: %w", err)
	}

	return &Publisher{ch: ch}, nil
}

// PublishPaymentRequest mengirim satu permintaan pembayaran dan tunggu sampai broker mengonfirmasi
func (p *Publisher) PublishPaymentRequest(ctx context.Context, msg PaymentRequestMessage) error {
	// Setiap request HTTP berjalan di goroutine sendiri, jadi publish harus antre satu persatu
	p.mu.Lock()
	defer p.mu.Unlock()
	return publishJSON(ctx, p.ch, RoutingKeyPayment, msg.PaymentID.String(), msg)
}

func (p *Publisher) Close() error {
	return p.ch.Close()
}
