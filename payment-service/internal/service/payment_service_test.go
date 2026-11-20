package service

import (
	"context"
	"errors"
	"payment-service/internal/model"
	"payment-service/internal/repository"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Part 3: unit test untuk Process (tanpa database, tanpa RabbitMQ, tanpa core sungguhan).

// ---- fake repository ----

type fakeRepo struct {
	rows map[uuid.UUID]model.Payment
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]model.Payment{}}
}

func (r *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (*model.Payment, error) {
	p, ok := r.rows[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &p, nil
}

func (r *fakeRepo) Create(_ context.Context, p *model.Payment) error {
	r.rows[p.ID] = *p
	return nil
}

func (r *fakeRepo) Save(_ context.Context, p *model.Payment) error {
	r.rows[p.ID] = *p
	return nil
}

// ---- fake core ----

type fakeCore struct {
	balance    int64
	debits     map[uuid.UUID]*DebitRecord // paymentID → debit
	down       bool
	debitCalls int
}

func (c *fakeCore) GetBalance(context.Context, uuid.UUID) (int64, error) {
	if c.down {
		return 0, ErrCoreUnavailable
	}
	return c.balance, nil
}

func (c *fakeCore) DebitForPayment(_ context.Context, userID uuid.UUID, amount int64, paymentID uuid.UUID, _ string) (int64, error) {
	c.debitCalls++
	if c.down {
		return 0, ErrCoreUnavailable
	}
	if _, ok := c.debits[paymentID]; ok {
		return c.balance, nil // replay: tidak dipotong lagi (meniru core yang idempoten)
	}
	if c.balance < amount {
		return 0, ErrInsufficientBalance
	}
	c.balance -= amount
	c.debits[paymentID] = &DebitRecord{TransactionID: "tx-" + paymentID.String(), UserID: userID, Amount: amount, CreatedAt: time.Now()}
	return c.balance, nil
}

func (c *fakeCore) FindDebit(_ context.Context, paymentID uuid.UUID) (*DebitRecord, error) {
	if c.down {
		return nil, ErrCoreUnavailable
	}
	d, ok := c.debits[paymentID]
	if !ok {
		return nil, ErrDebitNotFound
	}
	out := *d
	out.Balance = c.balance
	return &out, nil
}

// ---- helper ----

const testTTL = 10 * time.Minute

type fixture struct {
	svc   *paymentService
	repo  *fakeRepo
	core  *fakeCore
	clock time.Time
	user  uuid.UUID
}

func newFixture() *fixture {
	f := &fixture{
		repo:  newFakeRepo(),
		core:  &fakeCore{balance: 100_000, debits: map[uuid.UUID]*DebitRecord{}},
		clock: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		user:  uuid.New(),
	}
	svc := NewPaymentService(f.repo, f.core, testTTL, 0).(*paymentService)
	svc.now = func() time.Time { return f.clock }
	svc.sleep = func(context.Context, time.Duration) error { return nil } // test tidak perlu benar-benar menunggu
	f.svc = svc
	return f
}

// msg membuat pesan yang "dikirim core" pada waktu clock saat ini.
func (f *fixture) msg(amount int64) ProcessInput {
	return ProcessInput{PaymentID: uuid.New(), UserID: f.user, MerchantID: "m-1", Amount: amount, CreatedAt: f.clock}
}

func (f *fixture) process(t *testing.T, in ProcessInput) *ProcessResult {
	t.Helper()
	res, err := f.svc.Process(context.Background(), in)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	return res
}

// ---- tests ----

func TestProcess_Success_DebitsAndCompletes(t *testing.T) {
	f := newFixture()
	in := f.msg(30_000)

	res := f.process(t, in)

	if res.Status != model.PaymentCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if f.core.balance != 70_000 {
		t.Fatalf("saldo = %d, want 70000", f.core.balance)
	}
	if got := f.repo.rows[in.PaymentID]; got.Status != model.PaymentCompleted || got.PaidAt == nil {
		t.Fatalf("payment tersimpan = %+v", got)
	}
	if want := in.CreatedAt.Add(testTTL); !f.repo.rows[in.PaymentID].ExpiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", f.repo.rows[in.PaymentID].ExpiresAt, want)
	}
}

func TestProcess_Redelivery_DoesNotDebitTwice(t *testing.T) {
	f := newFixture()
	in := f.msg(30_000)

	f.process(t, in)
	res := f.process(t, in) // pesan yang sama datang lagi

	if res.Status != model.PaymentCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if f.core.debitCalls != 1 || f.core.balance != 70_000 {
		t.Fatalf("debitCalls = %d, saldo = %d; want 1 dan 70000", f.core.debitCalls, f.core.balance)
	}
}

func TestProcess_InsufficientBalance_Failed(t *testing.T) {
	f := newFixture()
	in := f.msg(500_000)

	res := f.process(t, in)

	if res.Status != model.PaymentFailed || res.Reason == "" {
		t.Fatalf("got %s %q, want failed dengan reason", res.Status, res.Reason)
	}
	if f.core.balance != 100_000 {
		t.Fatalf("saldo berubah: %d", f.core.balance)
	}
}

func TestProcess_OlderThan10Minutes_ExpiredWithoutDebit(t *testing.T) {
	f := newFixture()
	in := f.msg(30_000)
	f.clock = f.clock.Add(11 * time.Minute) // payment-service baru sempat memproses 11 menit kemudian

	res := f.process(t, in)

	if res.Status != model.PaymentExpired {
		t.Fatalf("status = %s, want expired", res.Status)
	}
	if f.core.debitCalls != 0 || f.core.balance != 100_000 {
		t.Fatalf("saldo tidak boleh dipotong: debitCalls=%d saldo=%d", f.core.debitCalls, f.core.balance)
	}
}

func TestProcess_CoreDown_ReturnsErrorAndStaysPending(t *testing.T) {
	f := newFixture()
	in := f.msg(30_000)
	f.core.down = true

	_, err := f.svc.Process(context.Background(), in)

	if !errors.Is(err, ErrCoreUnavailable) {
		t.Fatalf("err = %v, want ErrCoreUnavailable", err)
	}
	if got := f.repo.rows[in.PaymentID].Status; got != model.PaymentPending {
		t.Fatalf("status = %s, want pending", got)
	}

	// Core hidup lagi, pesan dikirim ulang → berhasil.
	f.core.down = false
	if res := f.process(t, in); res.Status != model.PaymentCompleted {
		t.Fatalf("retry: status = %s, want completed", res.Status)
	}
}

func TestProcess_DebitedButAnswerLost_ThenExpired_MarksCompleted(t *testing.T) {
	f := newFixture()
	in := f.msg(30_000)

	// Percobaan pertama: core memotong saldo, tapi jawabannya tidak sampai (timeout).
	f.repo.rows[in.PaymentID] = model.Payment{ID: in.PaymentID, UserID: in.UserID, MerchantID: "m-1", Amount: in.Amount,
		Status: model.PaymentPending, ExpiresAt: in.CreatedAt.Add(testTTL)}
	f.core.balance -= in.Amount
	f.core.debits[in.PaymentID] = &DebitRecord{TransactionID: "tx-lost", UserID: in.UserID, Amount: in.Amount, CreatedAt: f.clock}

	// Pesan baru diproses ulang setelah lewat 10 menit.
	f.clock = f.clock.Add(11 * time.Minute)
	res := f.process(t, in)

	if res.Status != model.PaymentCompleted {
		t.Fatalf("status = %s, want completed (saldo sudah terpotong)", res.Status)
	}
	if f.core.balance != 70_000 {
		t.Fatalf("saldo = %d, want 70000 (terpotong tepat sekali)", f.core.balance)
	}
}

func TestProcess_ExpiredButCoreDown_Retries(t *testing.T) {
	f := newFixture()
	in := f.msg(30_000)
	f.clock = f.clock.Add(11 * time.Minute)
	f.core.down = true

	if _, err := f.svc.Process(context.Background(), in); !errors.Is(err, ErrCoreUnavailable) {
		t.Fatalf("err = %v, want ErrCoreUnavailable (belum bisa dipastikan, jangan expired dulu)", err)
	}
	if got := f.repo.rows[in.PaymentID].Status; got != model.PaymentPending {
		t.Fatalf("status = %s, want pending", got)
	}
}

func TestProcess_InvalidMessage(t *testing.T) {
	f := newFixture()
	cases := map[string]func(*ProcessInput){
		"payment_id kosong": func(in *ProcessInput) { in.PaymentID = uuid.Nil },
		"user_id kosong":    func(in *ProcessInput) { in.UserID = uuid.Nil },
		"merchant kosong":   func(in *ProcessInput) { in.MerchantID = " " },
		"amount nol":        func(in *ProcessInput) { in.Amount = 0 },
		"created_at kosong": func(in *ProcessInput) { in.CreatedAt = time.Time{} },
	}
	for name, mutate := range cases {
		in := f.msg(10_000)
		mutate(&in)
		if _, err := f.svc.Process(context.Background(), in); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("%s: err = %v, want ErrInvalidMessage", name, err)
		}
	}
	if f.core.debitCalls != 0 {
		t.Fatalf("pesan tidak valid tidak boleh memotong saldo")
	}
}
