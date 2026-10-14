package platform

// A tiny, dependency-free Prometheus metrics implementation (text exposition format).
// The metric names match the Node.js and Python services so one Grafana dashboard
// covers all 10 services. Most teams use github.com/prometheus/client_golang; we keep
// the Go services at a single third-party dependency (the Postgres driver) to minimise
// supply-chain risk and image size.

import (
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type collector interface {
	write(w io.Writer)
}

// Registry holds all metrics exposed on /metrics.
type Registry struct {
	mu         sync.Mutex
	collectors []collector
	started    time.Time
}

func NewRegistry() *Registry { return &Registry{started: time.Now()} }

func (r *Registry) register(c collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, c)
}

// Write renders every metric in Prometheus text format.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	collectors := append([]collector(nil), r.collectors...)
	r.mu.Unlock()
	for _, c := range collectors {
		c.write(w)
	}
	r.writeRuntime(w)
}

func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func labelString(names, values []string, extra ...string) string {
	parts := make([]string, 0, len(names)+1)
	for i, n := range names {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, n, escapeLabel(values[i])))
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ---------------------------------------------------------------- counter

// CounterVec is a monotonically increasing counter with labels.
type CounterVec struct {
	mu     sync.Mutex
	name   string
	help   string
	labels []string
	series map[string]*counterSeries
}

type counterSeries struct {
	values []string
	value  float64
}

func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{name: name, help: help, labels: labels, series: map[string]*counterSeries{}}
	r.register(c)
	return c
}

func (c *CounterVec) Add(v float64, labelValues ...string) {
	key := strings.Join(labelValues, "\xff")
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.series[key]
	if !ok {
		s = &counterSeries{values: append([]string(nil), labelValues...)}
		c.series[key] = s
	}
	s.value += v
}

func (c *CounterVec) Inc(labelValues ...string) { c.Add(1, labelValues...) }

func (c *CounterVec) write(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	for _, key := range sortedKeys(c.series) {
		s := c.series[key]
		fmt.Fprintf(w, "%s%s %s\n", c.name, labelString(c.labels, s.values), formatFloat(s.value))
	}
}

// ---------------------------------------------------------------- histogram

// HistogramVec tracks distributions (e.g. latency) in cumulative buckets.
type HistogramVec struct {
	mu      sync.Mutex
	name    string
	help    string
	labels  []string
	buckets []float64
	series  map[string]*histSeries
}

type histSeries struct {
	values []string
	counts []uint64
	sum    float64
	count  uint64
}

func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	h := &HistogramVec{name: name, help: help, labels: labels, buckets: buckets, series: map[string]*histSeries{}}
	r.register(h)
	return h
}

func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	key := strings.Join(labelValues, "\xff")
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[key]
	if !ok {
		s = &histSeries{values: append([]string(nil), labelValues...), counts: make([]uint64, len(h.buckets))}
		h.series[key] = s
	}
	for i, upper := range h.buckets {
		if v <= upper {
			s.counts[i]++
		}
	}
	s.sum += v
	s.count++
}

func (h *HistogramVec) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	for _, key := range sortedKeys(h.series) {
		s := h.series[key]
		for i, upper := range h.buckets {
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, labelString(h.labels, s.values, fmt.Sprintf(`le="%s"`, formatFloat(upper))), s.counts[i])
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, labelString(h.labels, s.values, `le="+Inf"`), s.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, labelString(h.labels, s.values), formatFloat(s.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.name, labelString(h.labels, s.values), s.count)
	}
}

// ---------------------------------------------------------------- gauge

// GaugeFunc reports a value computed at scrape time.
type GaugeFunc struct {
	name, help string
	labels     []string
	values     []string
	fn         func() float64
}

func (r *Registry) NewGaugeFunc(name, help string, fn func() float64) {
	r.register(&GaugeFunc{name: name, help: help, fn: fn})
}

func (r *Registry) NewInfoGauge(name, help string, labels, values []string) {
	r.register(&GaugeFunc{name: name, help: help, labels: labels, values: values, fn: func() float64 { return 1 }})
}

func (g *GaugeFunc) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s%s %s\n", g.name, g.help, g.name, g.name, labelString(g.labels, g.values), formatFloat(g.fn()))
}

// ---------------------------------------------------------------- runtime

func (r *Registry) writeRuntime(w io.Writer) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Fprintf(w, "# HELP go_goroutines Number of goroutines.\n# TYPE go_goroutines gauge\ngo_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "# HELP go_memstats_heap_alloc_bytes Heap bytes allocated and in use.\n# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes %d\n", m.HeapAlloc)
	fmt.Fprintf(w, "# HELP process_start_time_seconds Start time of the process since unix epoch.\n# TYPE process_start_time_seconds gauge\nprocess_start_time_seconds %d\n", r.started.Unix())
	if cpu, rss, ok := procStats(); ok {
		fmt.Fprintf(w, "# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.\n# TYPE process_cpu_seconds_total counter\nprocess_cpu_seconds_total %s\n", formatFloat(cpu))
		fmt.Fprintf(w, "# HELP process_resident_memory_bytes Resident memory size in bytes.\n# TYPE process_resident_memory_bytes gauge\nprocess_resident_memory_bytes %d\n", rss)
	}
}

// procStats reads CPU seconds and RSS from /proc (Linux only; returns ok=false elsewhere).
func procStats() (cpuSeconds float64, rssBytes int64, ok bool) {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, 0, false
	}
	// Fields after the ")" that closes the process name.
	idx := strings.LastIndexByte(string(stat), ')')
	fields := strings.Fields(string(stat)[idx+2:])
	if len(fields) < 22 {
		return 0, 0, false
	}
	utime, _ := strconv.ParseFloat(fields[11], 64)
	stime, _ := strconv.ParseFloat(fields[12], 64)
	rssPages, _ := strconv.ParseInt(fields[21], 10, 64)
	const clockTicks = 100 // USER_HZ on Linux
	return (utime + stime) / clockTicks, rssPages * int64(os.Getpagesize()), true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
