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

// Step 14: unit test untuk keputusan rekonsiliasi (tanpa database dan tanpa core sungguhan).

// ---- fake repository + transactor (rollback disimulasikan dengan snapshot) ----

type fakeRepo struct {
	rows   map[uuid.UUID]model.Payment
	locked map[uuid.UUID]bool // baris yang "sedang dikunci confirm lain" → SKIP LOCKED
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]model.Payment{}, locked: map[uuid.UUID]bool{}}
}

func (r *fakeRepo) Create(_ context.Context, p *model.Payment) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	r.rows[p.ID] = *p
	return nil
}

func (r *fakeRepo) FindByIDForUpdate(_ context.Context, id, userID uuid.UUID) (*model.Payment, error) {
	p, ok := r.rows[id]
	if !ok || p.UserID != userID {
		return nil, repository.ErrNotFound
	}
	return &p, nil
}

func (r *fakeRepo) Save(_ context.Context, p *model.Payment) error {
	r.rows[p.ID] = *p
	return nil
}

func (r *fakeRepo) ListOverduePendingIDs(_ context.Context, before time.Time, _ int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for id, p := range r.rows {
		if p.Status == model.PaymentPending && !p.ExpiresAt.After(before) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (r *fakeRepo) LockPendingByID(_ context.Context, id uuid.UUID) (*model.Payment, error) {
	p, ok := r.rows[id]
	if !ok || p.Status != model.PaymentPending || r.locked[id] {
		return nil, repository.ErrNotFound
	}
	return &p, nil
}

type fakeTx struct{ repo *fakeRepo }

func (t fakeTx) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	snapshot := map[uuid.UUID]model.Payment{}
	for k, v := range t.repo.rows {
		snapshot[k] = v
	}
	if err := fn(ctx); err != nil {
		t.repo.rows = snapshot // ROLLBACK
		return err
	}
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
	if _, ok := c.debits[paymentID]; !ok {
		c.balance -= amount
		c.debits[paymentID] = &DebitRecord{TransactionID: "tx-" + paymentID.String(), UserID: userID, Amount: amount, CreatedAt: time.Now()}
	}
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

const (
	testTTL   = 10 * time.Minute
	testGrace = time.Minute
)

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
	svc := NewPaymentService(fakeTx{f.repo}, f.repo, f.core, testTTL, testGrace).(*paymentService)
	svc.now = func() time.Time { return f.clock }
	f.svc = svc
	return f
}

func (f *fixture) create(t *testing.T, amount int64) uuid.UUID {
	t.Helper()
	res, err := f.svc.Create(context.Background(), f.user, CreatePaymentInput{MerchantID: "m-1", Amount: amount})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Payment.ID
}

func (f *fixture) status(id uuid.UUID) model.PaymentStatus { return f.repo.rows[id].Status }

// "Debit sukses di core tapi payment-service tidak tahu" (timeout / commit payment_db gagal).
func (f *fixture) debitBehindOurBack(id uuid.UUID) {
	p := f.repo.rows[id]
	f.core.balance -= p.Amount
	f.core.debits[id] = &DebitRecord{TransactionID: "tx-lost", UserID: p.UserID, Amount: p.Amount, CreatedAt: f.clock}
}

// ---- tests: worker ----

func TestReconcile_DebitFound_MarksCompleted(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)
	f.debitBehindOurBack(id) // kasus #5: debit terjadi, user tidak pernah retry

	f.clock = f.clock.Add(testTTL + testGrace + time.Second)
	res, err := f.svc.ReconcileOverdue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed != 1 || f.status(id) != model.PaymentCompleted {
		t.Fatalf("want completed, got res=%+v status=%s", res, f.status(id))
	}
	if f.repo.rows[id].PaidAt == nil {
		t.Fatal("paid_at harus terisi dari waktu debit di core")
	}
}

func TestReconcile_NoDebit_MarksExpired(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)

	f.clock = f.clock.Add(testTTL + testGrace + time.Second)
	res, _ := f.svc.ReconcileOverdue(context.Background())
	if res.Expired != 1 || f.status(id) != model.PaymentExpired {
		t.Fatalf("want expired, got res=%+v status=%s", res, f.status(id))
	}
}

func TestReconcile_WithinGrace_NotTouched(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)

	f.clock = f.clock.Add(testTTL + testGrace/2) // sudah lewat expires_at, belum lewat grace
	res, _ := f.svc.ReconcileOverdue(context.Background())
	if res != (ReconcileResult{}) || f.status(id) != model.PaymentPending {
		t.Fatalf("dalam grace harus dibiarkan pending, got res=%+v status=%s", res, f.status(id))
	}
}

func TestReconcile_CoreDown_StaysPendingThenRecovers(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)
	f.debitBehindOurBack(id)
	f.clock = f.clock.Add(testTTL + testGrace + time.Second)

	f.core.down = true
	res, _ := f.svc.ReconcileOverdue(context.Background())
	if res.Failed != 1 || f.status(id) != model.PaymentPending {
		t.Fatalf("core mati: harus tetap pending, got res=%+v status=%s", res, f.status(id))
	}

	f.core.down = false
	res, _ = f.svc.ReconcileOverdue(context.Background())
	if res.Completed != 1 || f.status(id) != model.PaymentCompleted {
		t.Fatalf("setelah core hidup: harus completed, got res=%+v status=%s", res, f.status(id))
	}
}

func TestReconcile_LockedByConfirm_Skipped(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)
	f.repo.locked[id] = true
	f.clock = f.clock.Add(testTTL + testGrace + time.Second)

	res, _ := f.svc.ReconcileOverdue(context.Background())
	if res.Skipped != 1 || f.status(id) != model.PaymentPending {
		t.Fatalf("baris terkunci harus dilewati, got res=%+v status=%s", res, f.status(id))
	}
}

func TestReconcile_MismatchedDebit_NotCompleted(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)
	f.core.debits[id] = &DebitRecord{TransactionID: "tx-x", UserID: uuid.New(), Amount: 5_000}
	f.clock = f.clock.Add(testTTL + testGrace + time.Second)

	res, _ := f.svc.ReconcileOverdue(context.Background())
	if res.Failed != 1 || f.status(id) != model.PaymentPending {
		t.Fatalf("debit milik user lain tidak boleh menyelesaikan payment, got res=%+v status=%s", res, f.status(id))
	}
}

// ---- tests: confirm setelah expiry ----

func TestConfirmAfterExpiry_DebitFound_ReturnsCompleted(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)
	f.debitBehindOurBack(id) // confirm pertama timeout tapi core sudah memotong

	f.clock = f.clock.Add(testTTL + time.Second) // user retry SETELAH expired
	res, err := f.svc.Confirm(context.Background(), f.user, id)
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if res.Payment.Status != model.PaymentCompleted || res.NewBalance != 95_000 {
		t.Fatalf("got status=%s balance=%d", res.Payment.Status, res.NewBalance)
	}
	if f.core.debitCalls != 0 {
		t.Fatal("setelah expired tidak boleh memanggil debit lagi")
	}
}

func TestConfirmAfterExpiry_NoDebit_ExpiredWithoutCharging(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)

	f.clock = f.clock.Add(testTTL + time.Second) // masih dalam grace
	if _, err := f.svc.Confirm(context.Background(), f.user, id); !errors.Is(err, ErrPaymentExpired) {
		t.Fatalf("want ErrPaymentExpired, got %v", err)
	}
	if f.status(id) != model.PaymentPending {
		t.Fatalf("dalam grace status dibiarkan pending untuk worker, got %s", f.status(id))
	}

	f.clock = f.clock.Add(testGrace) // lewat grace
	if _, err := f.svc.Confirm(context.Background(), f.user, id); !errors.Is(err, ErrPaymentExpired) {
		t.Fatalf("want ErrPaymentExpired, got %v", err)
	}
	if f.status(id) != model.PaymentExpired || f.core.balance != 100_000 || f.core.debitCalls != 0 {
		t.Fatalf("got status=%s balance=%d debitCalls=%d", f.status(id), f.core.balance, f.core.debitCalls)
	}
}

func TestConfirmAfterExpiry_CoreDown_Unavailable(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)
	f.clock = f.clock.Add(testTTL + testGrace + time.Second)
	f.core.down = true

	if _, err := f.svc.Confirm(context.Background(), f.user, id); !errors.Is(err, ErrCoreUnavailable) {
		t.Fatalf("want ErrCoreUnavailable, got %v", err)
	}
	if f.status(id) != model.PaymentPending {
		t.Fatalf("tidak boleh berubah saat core mati, got %s", f.status(id))
	}
}

func TestConfirm_BeforeExpiry_DebitsOnce(t *testing.T) {
	f := newFixture()
	id := f.create(t, 5_000)

	res, err := f.svc.Confirm(context.Background(), f.user, id)
	if err != nil || res.Payment.Status != model.PaymentCompleted || res.NewBalance != 95_000 {
		t.Fatalf("got res=%+v err=%v", res, err)
	}
	if _, err := f.svc.Confirm(context.Background(), f.user, id); !errors.Is(err, ErrPaymentNotPending) {
		t.Fatalf("confirm kedua: want ErrPaymentNotPending, got %v", err)
	}
	if f.core.debitCalls != 1 {
		t.Fatalf("debit harus sekali, got %d", f.core.debitCalls)
	}
}
