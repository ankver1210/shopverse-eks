// ShopVerse Product Service (Go / PostgreSQL)
//
// Owns the product catalog. Read-heavy and stateless apart from its database,
// which makes it a great candidate for horizontal autoscaling (HPA).
//
//	GET /products?category=&search=&limit=   list products
//	GET /products/batch?ids=1,2,3            fetch several products at once (used by cart & order)
//	GET /products/{id}                       one product
//	GET /categories                          distinct categories
package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"shopverse/product-service/internal/platform"
)

const schema = `
CREATE TABLE IF NOT EXISTS products (
    id                 INTEGER PRIMARY KEY,
    name               TEXT NOT NULL,
    short_description  TEXT NOT NULL,
    description        TEXT NOT NULL,
    category           TEXT NOT NULL,
    emoji              TEXT NOT NULL,
    price_cents        INTEGER NOT NULL CHECK (price_cents > 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS products_category_idx ON products (category);`

// Product is the public representation of a catalog item.
type Product struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	ShortDescription string `json:"short_description"`
	Description      string `json:"description"`
	Category         string `json:"category"`
	Emoji            string `json:"emoji"`
	PriceCents       int    `json:"price_cents"`
}

type server struct {
	p     *platform.Platform
	db    *sql.DB
	views *platform.CounterVec
}

func main() {
	p := platform.New("product-service")
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

	s := &server{p: p, db: db, views: p.Metrics.NewCounterVec("product_views_total", "Product detail lookups", "product_id")}
	mux := http.NewServeMux()
	p.RegisterOps(mux, map[string]func(context.Context) error{"postgres": db.PingContext})
	mux.HandleFunc("GET /products", s.listProducts)
	mux.HandleFunc("GET /products/batch", s.batchProducts)
	mux.HandleFunc("GET /products/{id}", s.getProduct)
	mux.HandleFunc("GET /categories", s.listCategories)

	p.Run(p.Handler(mux), func() { _ = db.Close() })
}

func migrateAndSeed(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	for _, p := range seedProducts {
		// ON CONFLICT DO NOTHING: safe to run on every start, from every replica.
		_, err := db.ExecContext(ctx, `INSERT INTO products (id, name, short_description, description, category, emoji, price_cents)
			VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (id) DO NOTHING`,
			p.ID, p.Name, p.ShortDescription, p.Description, p.Category, p.Emoji, p.PriceCents)
		if err != nil {
			return err
		}
	}
	return nil
}

const productColumns = "id, name, short_description, description, category, emoji, price_cents"

func scanProducts(rows *sql.Rows) ([]Product, error) {
	defer rows.Close()
	products := []Product{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.ShortDescription, &p.Description, &p.Category, &p.Emoji, &p.PriceCents); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

func (s *server) listProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 100
	}
	query := "SELECT " + productColumns + " FROM products WHERE ($1 = '' OR category = $1) AND ($2 = '' OR name ILIKE '%' || $2 || '%') ORDER BY id LIMIT $3"
	rows, err := s.db.QueryContext(r.Context(), query, q.Get("category"), q.Get("search"), limit)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	products, err := scanProducts(rows)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"products": products, "count": len(products)})
}

func (s *server) batchProducts(w http.ResponseWriter, r *http.Request) {
	ids, err := parseIDs(r.URL.Query().Get("ids"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// lib/pq accepts a Postgres array literal for = ANY($1)
	rows, err := s.db.QueryContext(r.Context(), "SELECT "+productColumns+" FROM products WHERE id = ANY($1::int[]) ORDER BY id", intArray(ids))
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	products, err := scanProducts(rows)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"products": products})
}

func (s *server) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT "+productColumns+" FROM products WHERE id = $1", id)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	products, err := scanProducts(rows)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if len(products) == 0 {
		platform.WriteError(w, http.StatusNotFound, "product not found")
		return
	}
	s.views.Inc(strconv.Itoa(id))
	platform.WriteJSON(w, http.StatusOK, products[0])
}

func (s *server) listCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), "SELECT DISTINCT category FROM products ORDER BY category")
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	defer rows.Close()
	categories := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			s.dbError(w, r, err)
			return
		}
		categories = append(categories, c)
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func (s *server) dbError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return // client went away
	}
	s.p.Log(r.Context()).Error("database error", "error", err)
	platform.WriteError(w, http.StatusServiceUnavailable, "database unavailable")
}

// parseIDs turns "1,2,3" into []int{1,2,3} (max 100 ids, all positive).
func parseIDs(raw string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("ids query parameter is required, e.g. ?ids=1,2,3")
	}
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
