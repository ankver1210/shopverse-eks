// ShopVerse Inventory Service (Go / PostgreSQL)
//
// Tracks available stock and short-lived reservations made during checkout.
// Reservations are idempotent per order and use row locks (SELECT ... FOR UPDATE)
// so two customers can never buy the last item at the same time.
//
//	GET  /inventory?ids=1,2,3         stock for several products (public, via gateway)
//	GET  /inventory/{productId}       stock for one product      (public, via gateway)
//	POST /inventory/reserve           reserve stock for an order (internal: order-service)
//	POST /inventory/release           undo a reservation         (internal: saga compensation)
//	POST /inventory/commit            finalize a reservation     (internal)
//	POST /inventory/restock           top up stock               (internal: restock CronJob)
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"shopverse/inventory-service/internal/platform"
)

const schema = `
CREATE TABLE IF NOT EXISTS stock (
    product_id  INTEGER PRIMARY KEY,
    available   INTEGER NOT NULL CHECK (available >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS reservations (
    order_id    TEXT NOT NULL,
    product_id  INTEGER NOT NULL,
    quantity    INTEGER NOT NULL CHECK (quantity > 0),
    status      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (order_id, product_id)
);`

// Initial stock per product id. A few items are deliberately scarce so you can see
// "Only N left" and "Sold out" in the UI and test the out-of-stock saga path.
var seedStock = map[int]int{1: 500, 2: 8, 3: 500, 4: 500, 5: 500, 6: 500, 7: 500, 8: 500, 9: 5, 10: 500, 11: 500, 12: 3}

type item struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

type reserveRequest struct {
	OrderID string `json:"order_id"`
	Items   []item `json:"items"`
}

type orderRequest struct {
	OrderID string `json:"order_id"`
}

type restockRequest struct {
	ProductID int `json:"product_id"` // 0 = all products below their seed level
	Quantity  int `json:"quantity"`
}

type server struct {
	p            *platform.Platform
	db           *sql.DB
	reservations *platform.CounterVec
}

// insufficientStock is returned when a reservation cannot be satisfied.
type insufficientStock struct {
	ProductID, Requested, Available int
}

func (e *insufficientStock) Error() string {
	return fmt.Sprintf("Not enough stock for product %d (requested %d, available %d)", e.ProductID, e.Requested, e.Available)
}

func main() {
	p := platform.New("inventory-service")
	ctx := context.Background()
	db, err := p.ConnectPostgres(ctx)
	if err != nil {
		p.Logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
	if err := migrateAndSeed(ctx, db); err != nil {
		p.Logger.Error("migration failed", "error", err)
		os.Exit(1)
	}

	s := &server{p: p, db: db,
		reservations: p.Metrics.NewCounterVec("inventory_reservations_total", "Reservation attempts by result", "result")}
	p.Metrics.NewGaugeFunc("inventory_available_units", "Total units in stock across all products", func() float64 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var total sql.NullInt64
		_ = db.QueryRowContext(ctx, "SELECT sum(available) FROM stock").Scan(&total)
		return float64(total.Int64)
	})

	mux := http.NewServeMux()
	p.RegisterOps(mux, map[string]func(context.Context) error{"postgres": db.PingContext})
	mux.HandleFunc("GET /inventory", s.listStock)
	mux.HandleFunc("GET /inventory/{productId}", s.getStock)
	mux.HandleFunc("POST /inventory/reserve", s.reserve)
	mux.HandleFunc("POST /inventory/release", s.release)
	mux.HandleFunc("POST /inventory/commit", s.commit)
	mux.HandleFunc("POST /inventory/restock", s.restock)

	p.Run(p.Handler(mux), func() { _ = db.Close() })
}

func migrateAndSeed(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	for id, qty := range seedStock {
		if _, err := db.ExecContext(ctx, "INSERT INTO stock (product_id, available) VALUES ($1, $2) ON CONFLICT (product_id) DO NOTHING", id, qty); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) listStock(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ids")
	query := "SELECT product_id, available FROM stock ORDER BY product_id"
	args := []any{}
	if raw != "" {
		ids, err := parseIDs(raw)
		if err != nil {
			platform.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		query = "SELECT product_id, available FROM stock WHERE product_id = ANY($1::int[]) ORDER BY product_id"
		args = append(args, intArray(ids))
	}
	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]int{}
	for rows.Next() {
		var id, available int
		if err := rows.Scan(&id, &available); err != nil {
			s.fail(w, r, err)
			return
		}
		items = append(items, map[string]int{"product_id": id, "available": available})
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getStock(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("productId"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "productId must be an integer")
		return
	}
	var available int
	err = s.db.QueryRowContext(r.Context(), "SELECT available FROM stock WHERE product_id = $1", id).Scan(&available)
	if errors.Is(err, sql.ErrNoRows) {
		platform.WriteError(w, http.StatusNotFound, "unknown product")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]int{"product_id": id, "available": available})
}

func (s *server) reserve(w http.ResponseWriter, r *http.Request) {
	var req reserveRequest
	if err := platform.ReadJSON(w, r, &req); err != nil || req.OrderID == "" || len(req.Items) == 0 {
		platform.WriteError(w, http.StatusBadRequest, "order_id and at least one item are required")
		return
	}
	for _, it := range req.Items {
		if it.ProductID <= 0 || it.Quantity <= 0 {
			platform.WriteError(w, http.StatusBadRequest, "product_id and quantity must be positive")
			return
		}
	}
	// Always lock rows in the same order (by product id) to prevent deadlocks.
	sort.Slice(req.Items, func(i, j int) bool { return req.Items[i].ProductID < req.Items[j].ProductID })

	duplicate, err := s.reserveTx(r.Context(), req)
	var short *insufficientStock
	switch {
	case errors.As(err, &short):
		s.reservations.Inc("insufficient")
		platform.WriteJSON(w, http.StatusConflict, map[string]any{"error": short.Error(), "product_id": short.ProductID})
	case err != nil:
		s.fail(w, r, err)
	case duplicate:
		s.reservations.Inc("duplicate")
		platform.WriteJSON(w, http.StatusOK, map[string]string{"order_id": req.OrderID, "status": "RESERVED", "note": "already reserved"})
	default:
		s.reservations.Inc("reserved")
		s.p.Log(r.Context()).Info("stock reserved", "order_id", req.OrderID, "lines", len(req.Items))
		platform.WriteJSON(w, http.StatusCreated, map[string]string{"order_id": req.OrderID, "status": "RESERVED"})
	}
}

// reserveTx returns duplicate=true when this order already holds a reservation (idempotency).
func (s *server) reserveTx(ctx context.Context, req reserveRequest) (duplicate bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	var existing int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM reservations WHERE order_id = $1", req.OrderID).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		return true, tx.Commit()
	}
	for _, it := range req.Items {
		var available int
		err := tx.QueryRowContext(ctx, "SELECT available FROM stock WHERE product_id = $1 FOR UPDATE", it.ProductID).Scan(&available)
		if errors.Is(err, sql.ErrNoRows) {
			return false, &insufficientStock{ProductID: it.ProductID, Requested: it.Quantity, Available: 0}
		}
		if err != nil {
			return false, err
		}
		if available < it.Quantity {
			return false, &insufficientStock{ProductID: it.ProductID, Requested: it.Quantity, Available: available}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE stock SET available = available - $1, updated_at = now() WHERE product_id = $2", it.Quantity, it.ProductID); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO reservations (order_id, product_id, quantity, status) VALUES ($1, $2, $3, 'RESERVED')", req.OrderID, it.ProductID, it.Quantity); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
}

func (s *server) release(w http.ResponseWriter, r *http.Request) {
	var req orderRequest
	if err := platform.ReadJSON(w, r, &req); err != nil || req.OrderID == "" {
		platform.WriteError(w, http.StatusBadRequest, "order_id is required")
		return
	}
	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT product_id, quantity FROM reservations WHERE order_id = $1 AND status = 'RESERVED' ORDER BY product_id FOR UPDATE", req.OrderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var held []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ProductID, &it.Quantity); err != nil {
			rows.Close()
			s.fail(w, r, err)
			return
		}
		held = append(held, it)
	}
	rows.Close()
	for _, it := range held {
		if _, err := tx.ExecContext(ctx, "UPDATE stock SET available = available + $1, updated_at = now() WHERE product_id = $2", it.Quantity, it.ProductID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE reservations SET status = 'RELEASED', updated_at = now() WHERE order_id = $1 AND status = 'RESERVED'", req.OrderID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, r, err)
		return
	}
	s.p.Log(ctx).Info("reservation released", "order_id", req.OrderID, "lines", len(held))
	platform.WriteJSON(w, http.StatusOK, map[string]any{"order_id": req.OrderID, "released_lines": len(held)})
}

func (s *server) commit(w http.ResponseWriter, r *http.Request) {
	var req orderRequest
	if err := platform.ReadJSON(w, r, &req); err != nil || req.OrderID == "" {
		platform.WriteError(w, http.StatusBadRequest, "order_id is required")
		return
	}
	res, err := s.db.ExecContext(r.Context(), "UPDATE reservations SET status = 'COMMITTED', updated_at = now() WHERE order_id = $1 AND status = 'RESERVED'", req.OrderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, _ := res.RowsAffected()
	platform.WriteJSON(w, http.StatusOK, map[string]any{"order_id": req.OrderID, "committed_lines": n})
}

func (s *server) restock(w http.ResponseWriter, r *http.Request) {
	var req restockRequest
	if r.ContentLength != 0 {
		if err := platform.ReadJSON(w, r, &req); err != nil {
			platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	ctx := r.Context()
	if req.ProductID > 0 {
		if req.Quantity <= 0 || req.Quantity > 10000 {
			platform.WriteError(w, http.StatusBadRequest, "quantity must be between 1 and 10000")
			return
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE stock SET available = available + $1, updated_at = now() WHERE product_id = $2", req.Quantity, req.ProductID); err != nil {
			s.fail(w, r, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"restocked": []int{req.ProductID}})
		return
	}
	// Top every product back up to its seed level.
	restocked := []int{}
	for id, level := range seedStock {
		res, err := s.db.ExecContext(ctx, "UPDATE stock SET available = $1, updated_at = now() WHERE product_id = $2 AND available < $1", level, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if n, _ := res.RowsAffected(); n > 0 {
			restocked = append(restocked, id)
		}
	}
	sort.Ints(restocked)
	s.p.Log(ctx).Info("restock completed", "products", restocked)
	platform.WriteJSON(w, http.StatusOK, map[string]any{"restocked": restocked})
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.p.Log(r.Context()).Error("database error", "error", err)
	platform.WriteError(w, http.StatusServiceUnavailable, "database unavailable")
}

func parseIDs(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	if len(parts) > 100 {
		return nil, errors.New("at most 100 ids per request")
	}
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 {
			return nil, errors.New("ids must be positive integers")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func intArray(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
