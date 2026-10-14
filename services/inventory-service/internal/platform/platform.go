// Package platform is the shared "golden path" for every Go service in ShopVerse:
// JSON logging, RED metrics, request ids, chaos injection, health checks, PostgreSQL
// connection with retry, and graceful shutdown.
package platform

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver for database/sql
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

var skipPaths = map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}

// Platform bundles cross-cutting concerns for one service.
type Platform struct {
	Service      string
	Version      string
	Pod          string
	Logger       *slog.Logger
	Metrics      *Registry
	shuttingDown atomic.Bool

	httpRequests *CounterVec
	httpDuration *HistogramVec
	chaosErrors  float64
	chaosLatency time.Duration
}

// New creates the platform using SERVICE_NAME, APP_VERSION, POD_NAME, LOG_LEVEL and CHAOS_* env vars.
func New(defaultName string) *Platform {
	name := Env("SERVICE_NAME", defaultName)
	version := Env("APP_VERSION", "dev")
	host, _ := os.Hostname()
	pod := Env("POD_NAME", host)

	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(Env("LOG_LEVEL", "info")))
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				a.Key = "timestamp"
			case slog.LevelKey:
				a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
			}
			return a
		},
	})
	logger := slog.New(handler).With("service", name, "version", version, "pod", pod)

	reg := NewRegistry()
	p := &Platform{
		Service: name, Version: version, Pod: pod, Logger: logger, Metrics: reg,
		httpRequests: reg.NewCounterVec("http_requests_total", "Total HTTP requests handled", "method", "route", "status_code"),
		httpDuration: reg.NewHistogramVec("http_request_duration_seconds", "HTTP request latency in seconds",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}, "method", "route", "status_code"),
	}
	reg.NewInfoGauge("app_build_info", "Build information (always 1)", []string{"version"}, []string{version})
	p.chaosErrors, _ = strconv.ParseFloat(Env("CHAOS_ERROR_RATE", "0"), 64)
	latencyMs, _ := strconv.Atoi(Env("CHAOS_LATENCY_MS", "0"))
	p.chaosLatency = time.Duration(latencyMs) * time.Millisecond
	if p.chaosErrors > 0 || p.chaosLatency > 0 {
		logger.Warn("CHAOS MODE ENABLED", "error_rate", p.chaosErrors, "latency_ms", latencyMs)
	}
	return p
}

// Env returns the environment variable or a default.
func Env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Log returns a logger that includes the request id from ctx.
func (p *Platform) Log(ctx context.Context) *slog.Logger {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return p.Logger.With("request_id", id)
	}
	return p.Logger
}

// RequestID returns the request id stored in ctx (for outgoing calls).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randFloat() float64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return float64(n.Int64()) / 1_000_000
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

// Handler wraps a ServeMux with request ids, chaos, access logs and RED metrics.
func (p *Platform) Handler(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newID()
		}
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Service-Version", p.Service+"@"+p.Version)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		route := ""
		if !skipPaths[r.URL.Path] && (p.chaosErrors > 0 || p.chaosLatency > 0) {
			time.Sleep(p.chaosLatency)
			if p.chaosErrors > 0 && randFloat() < p.chaosErrors {
				route = "chaos"
				WriteJSON(rec, http.StatusInternalServerError, map[string]string{"error": "chaos: injected failure", "service": p.Service})
			}
		}
		if route == "" {
			mux.ServeHTTP(rec, r)
			// ServeMux records the matched pattern, e.g. "GET /products/{id}"
			route = r.Pattern
			if i := strings.IndexByte(route, ' '); i >= 0 {
				route = route[i+1:]
			}
			if route == "" {
				route = "unmatched"
			}
		}

		elapsed := time.Since(start)
		status := strconv.Itoa(rec.status)
		p.httpRequests.Inc(r.Method, route, status)
		p.httpDuration.Observe(elapsed.Seconds(), r.Method, route, status)
		if !skipPaths[r.URL.Path] {
			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			} else if rec.status >= 400 {
				level = slog.LevelWarn
			}
			p.Log(r.Context()).Log(r.Context(), level, "request completed",
				"method", r.Method, "path", r.URL.RequestURI(), "status", rec.status, "duration_ms", elapsed.Milliseconds())
		}
	})
}

// HealthBody is the JSON returned by /healthz and /readyz on every service.
func (p *Platform) HealthBody(status string, checks map[string]string) map[string]any {
	body := map[string]any{"status": status, "service": p.Service, "version": p.Version, "pod": p.Pod}
	if checks != nil {
		body["checks"] = checks
	}
	return body
}

// RegisterOps adds /healthz, /readyz and /metrics to the mux.
// readyChecks are dependencies this service OWNS (e.g. its database).
func (p *Platform) RegisterOps(mux *http.ServeMux, readyChecks map[string]func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, p.HealthBody("ok", nil))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if p.shuttingDown.Load() {
			WriteJSON(w, http.StatusServiceUnavailable, p.HealthBody("shutting_down", nil))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		checks, ready := map[string]string{}, true
		for name, check := range readyChecks {
			if err := check(ctx); err != nil {
				checks[name], ready = err.Error(), false
			} else {
				checks[name] = "ok"
			}
		}
		if !ready {
			WriteJSON(w, http.StatusServiceUnavailable, p.HealthBody("not_ready", checks))
			return
		}
		WriteJSON(w, http.StatusOK, p.HealthBody("ready", checks))
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		p.Metrics.Write(w)
	})
}

// Run starts the HTTP server and blocks until SIGTERM/SIGINT, then shuts down gracefully.
func (p *Platform) Run(handler http.Handler, onShutdown func()) {
	srv := &http.Server{
		Addr:              ":" + Env("PORT", "8080"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       65 * time.Second, // longer than the ALB idle timeout (60s)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	go func() {
		p.Logger.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.Logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	p.shuttingDown.Store(true) // readiness now fails -> no new traffic
	p.Logger.Info("shutdown signal received, draining connections")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		p.Logger.Error("graceful shutdown failed", "error", err)
	}
	if onShutdown != nil {
		onShutdown()
	}
	p.Logger.Info("shutdown complete")
}

// ConnectPostgres opens a pool using DB_* env vars and waits until the DB answers.
func (p *Platform) ConnectPostgres(ctx context.Context) (*sql.DB, error) {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(Env("DB_USER", "app"), Env("DB_PASSWORD", "")),
		Host:     net.JoinHostPort(Env("DB_HOST", "localhost"), Env("DB_PORT", "5432")),
		Path:     "/" + Env("DB_NAME", "app"),
		RawQuery: url.Values{"sslmode": {Env("DB_SSLMODE", "disable")}, "connect_timeout": {"5"}}.Encode(),
	}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		return nil, err
	}
	maxOpen, _ := strconv.Atoi(Env("DB_POOL_MAX", "10"))
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen / 2)
	db.SetConnMaxLifetime(30 * time.Minute)
	for attempt := 1; attempt <= 30; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = db.PingContext(pingCtx)
		cancel()
		if err == nil {
			p.Logger.Info("connected to postgres")
			return db, nil
		}
		p.Logger.Warn("database not ready yet", "attempt", attempt, "error", err)
		time.Sleep(time.Duration(min(2*attempt, 5)) * time.Second)
	}
	return nil, fmt.Errorf("postgres unreachable: %w", err)
}

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes {"error": msg} — the same error shape used by every ShopVerse service.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// ReadJSON decodes a JSON body (max 64KB) into v.
func ReadJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	return json.NewDecoder(r.Body).Decode(v)
}
