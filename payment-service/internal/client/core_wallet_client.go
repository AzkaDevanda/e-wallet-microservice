package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"payment-service/internal/service"
	"strings"
	"time"

	"github.com/google/uuid"
)

const internalAPIKeyHeader = "X-Internal-API-Key"

// CoreWalletClient memanggil endpoint /internal/wallets/... milik core-service lewat HTTP
// Ia memenuhi interface service.WalletGateway, jadi paymentService tidak tahu
// (dan tidak peduli) bahwa saldo sekarang ada di service lain
type CoreWalletClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewCoreWalletClient(baseURL, apiKey string, timeout time.Duration) *CoreWalletClient {
	return &CoreWalletClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		// Timeout WAJIB: tanpa ini, core yang macet membuat request (dan lock payment) tertahan selamanya
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Bentuk JSON sesuai kontrak internal core-service
type balanceResponse struct {
	UserID  string `json:"userID"`
	Balance int64  `json:"balance"`
}

type debitRequest struct {
	Amount      int64  `json:"amount"`
	ReferenceID string `json:"referenceID"`
	MerchantID  string `json:"merchantID"`
}

type debitResponse struct {
	TransactionID string `json:"transactionID"`
	NewBalance    int64  `json:"newBalance"`
	Replayed      bool   `json:"replayed"`
}

type debitRecordResponse struct {
	TransactionID string    `json:"transactionID"`
	UserID        uuid.UUID `json:"userID"`
	Amount        int64     `json:"amount"`
	MerchantID    string    `json:"merchantID"`
	CreatedAt     time.Time `json:"createdAt"`
	Balance       int64     `json:"balance"`
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// GetBalance Implements [service.WalletGateway]
func (c *CoreWalletClient) GetBalance(ctx context.Context, userID uuid.UUID) (int64, error) {
	var out balanceResponse
	path := "/internal/wallets/" + userID.String() + "/balance"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return 0, err
	}
	return out.Balance, nil
}

// DebitForPayment implements [service.WalletGateway]
// referenceID = paymentID, sehingga confirm ulang untuk payment yang sama tidak memotong dua kali
func (c *CoreWalletClient) DebitForPayment(ctx context.Context, userID uuid.UUID, amount int64, paymentID uuid.UUID, merchantID string) (int64, error) {
	var out debitResponse
	path := "/internal/wallets/" + userID.String() + "/debit"
	body := debitRequest{
		Amount:      amount,
		ReferenceID: paymentID.String(),
		MerchantID:  merchantID,
	}
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return 0, err
	}
	if out.Replayed {
		log.Printf("debit untuk payment %s sudah pernah diproses core (replayed), saldo tidak dipotong lagi", paymentID)
	}
	return out.NewBalance, nil
}

// FindDebit implements [service.WalletGateway] (Step 14)
func (c *CoreWalletClient) FindDebit(ctx context.Context, paymentID uuid.UUID) (*service.DebitRecord, error) {
	var out debitRecordResponse
	path := "/internal/debits/" + paymentID.String()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &service.DebitRecord{
		TransactionID: out.TransactionID,
		UserID:        out.UserID,
		Amount:        out.Amount,
		MerchantID:    out.MerchantID,
		CreatedAt:     out.CreatedAt,
		Balance:       out.Balance,
	}, nil
}

// do mengirim request ke core lalu menerjemahkan hasilnya:
// - 200 -> decode body ke out
// - code INSUFFICIENT_BALANCE -> service.ErrInsufficientBalance
// - code WALLET_NOT_FOUND -> service.ErrWalletNotFound
// - code DEBIT_NOT_FOUND -> service.ErrDebitNotFound (Step 14)
// - gagal koneksi/ time out / 5xx -> service.ErrCoreUnavailable (aman untuk dicoba lagi)
// - lainnya (401,409,400) -> error biasa: ada yang salah di konfigurasi atau kode
func (c *CoreWalletClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set(internalAPIKeyHeader, c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Core mati, alamat salah, atau timeout. Untuk debit: request mungkin sudah diproses core
		return fmt.Errorf("%w: %s %s: %v", service.ErrCoreUnavailable, method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			// Core sudah menjawab 200 (debit mungkin sudah terjadi), tapi body-nya rusak
			return fmt.Errorf("%w: decode response %s: %v", service.ErrCoreUnavailable, path, err)
		}
		return nil
	}

	var e errorResponse
	_ = json.NewDecoder(resp.Body).Decode(&e) // body error boleh kosong/rusak; cukup pakai status code

	switch e.Code {
	case "INSUFFICIENT_BALANCE":
		return service.ErrInsufficientBalance
	case "WALLET_NOT_FOUND":
		return service.ErrWalletNotFound
	case "DEBIT_NOT_FOUND":
		return service.ErrDebitNotFound
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: %s %s: status %d %s", service.ErrCoreUnavailable, method, path, resp.StatusCode, e.Code)
	}
	return fmt.Errorf("core %s %s: status %d %s: %s", method, path, resp.StatusCode, e.Code, e.Error)
}
