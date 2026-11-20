package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	connectAttempts = 10
	connectWait     = 3 * time.Second
)

// Connect membuka koneksi ke RabbitMQ. Dicoba beberapa kali,
// karena saat semua container baru nyala,
// RabbitMQ bisa lebih lambat siap dibanding service Go
func Connect(url string) (*amqp.Connection, error) {
	var lastErr error
	for attempt := 1; attempt <= connectAttempts; attempt++ {
		conn, err := amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		log.Printf("rabbitmq belum siap (percobaan %d/%d): %v", attempt, connectAttempts, err)
		time.Sleep(connectWait)
	}
	return nil, fmt.Errorf("connect rabbitmq: %w", lastErr)
}

// SetupTopology memastikan exchange, queue, dan binding sudah ada
// "Declare" bersifat idempotent: kalau sudah ada dengan settingan yang sama, tidak terjadi apa-apa
// karena itu core dan payment sama-sama memanggilnya, dan urutan start tidak penting
func SetupTopology(conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	err = ch.ExchangeDeclare(
		ExchangePayment,     // nama
		amqp.ExchangeDirect, // jenis: direct (routing key harus sama persis)
		true,                // durable : tetap ada setelah RabbitMQ restart
		false,               // auto-delete
		false,               // internal
		false,               // no wait
		nil,                 // argumen
	)

	if err != nil {
		return fmt.Errorf("declare exchange %s: %w", ExchangePayment, err)
	}

	bindings := []struct{ queue, key string }{
		{QueuePayment, RoutingKeyPayment},
		{QueueStatus, RoutingKeyStatus},
	}

	for _, b := range bindings {
		_, err := ch.QueueDeclare(
			b.queue, // nama
			true,    // durable
			false,   // auto-delete
			false,   // exclusive
			false,   // no-wait
			nil,     // argumen
		)
		if err != nil {
			return fmt.Errorf("declare queue %s: %w", b.queue, err)
		}
		if err := ch.QueueBind(b.queue, b.key, ExchangePayment, false, nil); err != nil {
			return fmt.Errorf("bind queue %s: %w", b.queue, err)
		}
	}

	return nil
}

// publishJSON mengirim v sebagai JSON ke payment.exchange
// lalu MENUNGGU konfirmasi broker
// ch harus sudah dalam confirm mode (ch.Confirm(false))
// Kalau fungsi ini mengembalikan nil, pesan sudah diterima dan disimpan RabbitMQ
func publishJSON(ctx context.Context, ch *amqp.Channel, routingKey, messageID string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx,
		ExchangePayment, // exchange
		routingKey,      // routing key
		false,           // Mandatory
		false,           // immediate (sudah tidak didukung RabbitMQ, selalu false)
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // disimpan ke disk
			MessageId:    messageID,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	acked, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait publish confirm: %w", err)
	}
	if !acked {
		return errors.New("broker menolak pesan (nack)")
	}
	return nil
}
