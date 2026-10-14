package platform

import (
	"strings"
	"testing"
)

func TestMetricsExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("demo_total", "Demo counter", "route")
	c.Inc("/a")
	c.Add(2, "/a")
	h := r.NewHistogramVec("demo_seconds", "Demo histogram", []float64{0.1, 1}, "route")
	h.Observe(0.05, "/a")
	h.Observe(0.5, "/a")

	var sb strings.Builder
	r.Write(&sb)
	out := sb.String()
	for _, want := range []string{
		`demo_total{route="/a"} 3`,
		`demo_seconds_bucket{route="/a",le="0.1"} 1`,
		`demo_seconds_bucket{route="/a",le="1"} 2`,
		`demo_seconds_bucket{route="/a",le="+Inf"} 2`,
		`demo_seconds_count{route="/a"} 2`,
		"# TYPE go_goroutines gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestLabelEscaping(t *testing.T) {
	if got := escapeLabel(`a"b\c`); got != `a\"b\\c` {
		t.Fatalf("got %s", got)
	}
}
