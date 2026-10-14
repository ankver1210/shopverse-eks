// ShopVerse Payment Service (Go / PostgreSQL)
//
// A SIMULATED payment processor — no real card data is ever handled. Payment
// "methods" are test tokens (like Stripe's test mode): tok_visa is approved and
// tok_declined is declined with HTTP 402.
//
// Production patterns shown here:
//
//   - Idempotency: charging the same order twice returns the original result.
//   - Not exposed publicly: only order-service may call it (NetworkPolicy + gateway routes).
//   - Realistic latency so you can see it on dashboards.
//
// Endpoints:
//
//	POST /payments/charge    {order_id, user_id, amount_cents, payment_method}
//	POST /payments/refund    {order_id}
//	GET  /payments/{orderId}
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"os"
	"time"

	"shopverse/payment-service/internal/platform"
)

const schema = `
CREATE TABLE IF NOT EXISTS payments (
    id              TEXT PRIMARY KEY,
    order_id        TEXT NOT NULL UNIQUE,
    user_id         TEXT NOT NULL,
    amount_cents    INTEGER NOT NULL CHECK (amount_cents > 0),
    payment_method  TEXT NOT NULL,
    status          TEXT NOT NULL,
    decline_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);`

// MaxAmountCents is the fraud-rule limit for a single charge ($5,000).
const MaxAmountCents = 500_000

type chargeRequest struct {
	OrderID       string `json:"order_id"`
	UserID        string `json:"user_id"`
	AmountCents   int    `json:"amount_cents"`
	PaymentMethod string `json:"payment_method"`
}

// Payment is the stored and returned payment record.
type Payment struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	UserID        string    `json:"user_id"`
	AmountCents   int       `json:"amount_cents"`
	PaymentMethod string    `json:"payment_method"`
	Status        string    `json:"status"`
	DeclineReason *string   `json:"decline_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type server struct {
	p        *platform.Platform
	db       *sql.DB
	payments *platform.CounterVec
	amount   *platform.CounterVec
}

func main() {
	p := platform.New("payment-service")
	ctx := context.Background()
	db, err := p.ConnectPostgres(ctx)
	if err != nil {
		p.Logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		p.Logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
	s := &server{
		p: p, db: db,
		payments: p.Metrics.NewCounterVec("payments_total", "Payment attempts by status", "status"),
		amount:   p.Metrics.NewCounterVec("payments_amount_cents_total", "Amount processed by status, in cents", "status"),
	}
	mux := http.NewServeMux()
	p.RegisterOps(mux, map[string]func(context.Context) error{"postgres": db.PingContext})
	mux.HandleFunc("POST /payments/charge", s.charge)
	mux.HandleFunc("POST /payments/refund", s.refund)
	mux.HandleFunc("GET /payments/{orderId}", s.get)
	p.Run(p.Handler(mux), func() { _ = db.Close() })
}

// decide applies the (simulated) bank + fraud rules. Returns "" when approved.
func decide(req chargeRequest) string {
	switch {
	case req.PaymentMethod == "tok_declined":
		return "card declined by issuer"
	case req.AmountCents > MaxAmountCents:
		return "amount exceeds fraud limit"
	case req.PaymentMethod != "tok_visa" && req.PaymentMethod != "tok_mastercard":
		return "unsupported payment method"
	}
	return ""
}

func newPaymentID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "pay_" + hex.EncodeToString(b)
}

func (s *server) charge(w http.ResponseWriter, r *http.Request) {
	var req chargeRequest
	if err := platform.ReadJSON(w, r, &req); err != nil || req.OrderID == "" || req.UserID == "" || req.AmountCents <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "order_id, user_id and a positive amount_cents are required")
		return
	}
	ctx := r.Context()

	// Idempotency: a retried request for the same order returns the original result.
	if existing, err := s.find(ctx, req.OrderID); err == nil {
		s.respond(w, existing, http.StatusOK)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.fail(w, r, err)
		return
	}

	// Simulate the round-trip to a card network (100-300ms).
	jitter, _ := rand.Int(rand.Reader, big.NewInt(200))
	select {
	case <-time.After(time.Duration(100+jitter.Int64()) * time.Millisecond):
	case <-ctx.Done():
		return
	}

	status, reason := "APPROVED", decide(req)
	var reasonPtr *string
	if reason != "" {
		status, reasonPtr = "DECLINED", &reason
	}
	payment := Payment{ID: newPaymentID(), OrderID: req.OrderID, UserID: req.UserID, AmountCents: req.AmountCents,
		PaymentMethod: req.PaymentMethod, Status: status, DeclineReason: reasonPtr}
	err := s.db.QueryRowContext(ctx, `INSERT INTO payments (id, order_id, user_id, amount_cents, payment_method, status, decline_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (order_id) DO NOTHING RETURNING created_at`,
		payment.ID, payment.OrderID, payment.UserID, payment.AmountCents, payment.PaymentMethod, payment.Status, payment.DeclineReason).Scan(&payment.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// A concurrent request for the same order won the race; return its result.
		existing, findErr := s.find(ctx, req.OrderID)
		if findErr != nil {
			s.fail(w, r, findErr)
			return
		}
		s.respond(w, existing, http.StatusOK)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.payments.Inc(status)
	s.amount.Add(float64(payment.AmountCents), status)
	s.p.Log(ctx).Info("payment processed", "order_id", payment.OrderID, "status", status, "amount_cents", payment.AmountCents)
	s.respond(w, payment, http.StatusCreated)
}

func (s *server) respond(w http.ResponseWriter, p Payment, okStatus int) {
	if p.Status == "DECLINED" {
		platform.WriteJSON(w, http.StatusPaymentRequired, map[string]any{"error": "payment declined", "payment": p})
		return
	}
	platform.WriteJSON(w, okStatus, p)
}

func (s *server) refund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrderID string `json:"order_id"`
	}
	if err := platform.ReadJSON(w, r, &req); err != nil || req.OrderID == "" {
		platform.WriteError(w, http.StatusBadRequest, "order_id is required")
		return
	}
	res, err := s.db.ExecContext(r.Context(), "UPDATE payments SET status = 'REFUNDED', updated_at = now() WHERE order_id = $1 AND status = 'APPROVED'", req.OrderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.payments.Inc("REFUNDED")
		s.p.Log(r.Context()).Info("payment refunded", "order_id", req.OrderID)
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"order_id": req.OrderID, "refunded": n > 0})
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	p, err := s.find(r.Context(), r.PathValue("orderId"))
	if errors.Is(err, sql.ErrNoRows) {
		platform.WriteError(w, http.StatusNotFound, "payment not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, p)
}

func (s *server) find(ctx context.Context, orderID string) (Payment, error) {
	var p Payment
	err := s.db.QueryRowContext(ctx, `SELECT id, order_id, user_id, amount_cents, payment_method, status, decline_reason, created_at
		FROM payments WHERE order_id = $1`, orderID).
		Scan(&p.ID, &p.OrderID, &p.UserID, &p.AmountCents, &p.PaymentMethod, &p.Status, &p.DeclineReason, &p.CreatedAt)
	return p, err
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.p.Log(r.Context()).Error("database error", "error", err)
	platform.WriteError(w, http.StatusServiceUnavailable, "database unavailable")
}
