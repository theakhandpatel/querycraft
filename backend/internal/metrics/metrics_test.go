package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestCounterAndGauge(t *testing.T) {
	r := New()
	r.Inc("queries_total", Labels{"status": "succeeded"}, "queries by status")
	r.Inc("queries_total", Labels{"status": "succeeded"}, "queries by status")
	r.Inc("queries_total", Labels{"status": "failed"}, "queries by status")

	if v, ok := r.Value("queries_total", Labels{"status": "succeeded"}); !ok || v != 2 {
		t.Fatalf("succeeded = %v,%v want 2,true", v, ok)
	}
	if v, ok := r.Value("queries_total", Labels{"status": "failed"}); !ok || v != 1 {
		t.Fatalf("failed = %v,%v want 1,true", v, ok)
	}
	// An unlabelled read of a labelled metric must not invent a series.
	if _, ok := r.Value("queries_total", nil); ok {
		t.Fatal("read of a nonexistent series reported ok")
	}

	r.Set("pool_connections", Labels{"state": "idle"}, 3, "pool")
	r.Set("pool_connections", Labels{"state": "idle"}, 5, "pool")
	if v, _ := r.Value("pool_connections", Labels{"state": "idle"}); v != 5 {
		t.Fatalf("gauge = %v want 5 (Set replaces)", v)
	}
}

func TestHistogramCountsAndSum(t *testing.T) {
	r := New()
	l := Labels{"route": "/query"}
	r.Observe("req_seconds", l, 0.003, "requests", nil) // bucket 0.005
	r.Observe("req_seconds", l, 1.5, "requests", nil)   // bucket 2.5
	r.Observe("req_seconds", l, 99, "requests", nil)    // +Inf

	count, sum, ok := r.Histogram("req_seconds", l)
	if !ok || count != 3 {
		t.Fatalf("count = %d,%v want 3,true", count, ok)
	}
	if sum <= 1.5 {
		t.Fatalf("sum = %v want the three observations summed", sum)
	}

	out := render(t, r)
	// Cumulative buckets: the 0.001 bucket holds nothing, 0.005 holds one, and
	// +Inf holds all three — never a decreasing count.
	if !strings.Contains(out, `req_seconds_bucket{le="0.001",route="/query"} 0`) {
		t.Fatalf("missing empty 0.001 bucket:\n%s", out)
	}
	if !strings.Contains(out, `req_seconds_bucket{le="0.005",route="/query"} 1`) {
		t.Fatalf("missing 0.005 bucket:\n%s", out)
	}
	if !strings.Contains(out, `req_seconds_bucket{le="2.5",route="/query"} 2`) {
		t.Fatalf("missing cumulative 2.5 bucket (want 2):\n%s", out)
	}
	if !strings.Contains(out, `req_seconds_bucket{le="+Inf",route="/query"} 3`) {
		t.Fatalf("missing +Inf bucket:\n%s", out)
	}
	if !strings.Contains(out, `req_seconds_count{route="/query"} 3`) {
		t.Fatalf("missing count:\n%s", out)
	}
}

func TestPrometheusShape(t *testing.T) {
	r := New()
	r.Inc("querycraft_queries_total", Labels{"status": "succeeded", "code": "ok"}, "queries by status")
	r.Set("querycraft_build_info", Labels{"version": "abc"}, 1, "build")

	out := render(t, r)
	if strings.Count(out, "# TYPE querycraft_queries_total counter") != 1 {
		t.Fatalf("TYPE must appear exactly once per name:\n%s", out)
	}
	if !strings.Contains(out, `# HELP querycraft_queries_total queries by status`) {
		t.Fatalf("missing HELP:\n%s", out)
	}
	// Labels are sorted so the text is stable across runs.
	if !strings.Contains(out, `querycraft_queries_total{code="ok",status="succeeded"} 1`) {
		t.Fatalf("labels not rendered sorted:\n%s", out)
	}
	if !strings.Contains(out, `querycraft_build_info{version="abc"} 1`) {
		t.Fatalf("gauge not rendered:\n%s", out)
	}
	// An unlabelled series has no braces at all.
	r2 := New()
	r2.Inc("bare_total", nil, "bare")
	if got := render(t, r2); !strings.Contains(got, "bare_total 1") {
		t.Fatalf("unlabelled series should not carry braces:\n%s", got)
	}
}

func TestLabelsWithEmptyValueAreAbsent(t *testing.T) {
	r := New()
	r.Inc("mixed_total", Labels{"status": "ok", "code": ""}, "mixed")
	out := render(t, r)
	if strings.Contains(out, "code=") {
		t.Fatalf("empty label value should be omitted:\n%s", out)
	}
	if !strings.Contains(out, `mixed_total{status="ok"} 1`) {
		t.Fatalf("expected only the non-empty label:\n%s", out)
	}
}

func TestNamesAndConcurrency(t *testing.T) {
	r := New()
	// Values and label characters that would break a naive renderer.
	r.Inc("escaped_total", Labels{"detail": `say "hi"\now`}, "escaped")
	out := render(t, r)
	if !strings.Contains(out, `detail="say \"hi\"\\now"`) {
		t.Fatalf("label escaping wrong:\n%s", out)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Inc("concurrent_total", nil, "c")
			r.Observe("concurrent_seconds", nil, 0.01, "h", nil)
		}()
	}
	wg.Wait()
	if v, _ := r.Value("concurrent_total", nil); v != 50 {
		t.Fatalf("concurrent counter = %v want 50", v)
	}
	if c, _, _ := r.Histogram("concurrent_seconds", nil); c != 50 {
		t.Fatalf("concurrent histogram count = %d want 50", c)
	}
	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("Names = %v want 3 entries", names)
	}
	if names[0] >= names[1] {
		t.Fatalf("Names not sorted: %v", names)
	}
}

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := r.WritePrometheus(&b); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	return b.String()
}

// A service that declares its instrumentation still describes itself before
// anything has happened: the diagnostics view must not say "no metrics" on a
// fresh process, and a scrape must carry the HELP/TYPE the metric will have.
func TestDeclaredMetricsAppearBeforeFirstEvent(t *testing.T) {
	r := New()
	r.DescribeCounter("qc_things_total", "Things counted.")
	r.DescribeGauge("qc_ready", "Readiness.")
	r.DescribeHistogram("qc_seconds", "Latency.")

	if v, ok := r.Value("qc_things_total", nil); ok {
		t.Fatalf("describing a counter created a series with value %v", v)
	}
	names := r.Names()
	want := []string{"qc_ready", "qc_seconds", "qc_things_total"}
	if len(names) != len(want) {
		t.Fatalf("Names = %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Names = %v want %v", names, want)
		}
	}

	body := render(t, r)
	for _, want := range []string{
		"# HELP qc_things_total Things counted.\n# TYPE qc_things_total counter",
		"# HELP qc_ready Readiness.\n# TYPE qc_ready gauge",
		"# HELP qc_seconds Latency.\n# TYPE qc_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("declared metric missing from exposition %q:\n%s", want, body)
		}
	}
	// A declared metric with no observations must not fabricate a sample.
	if strings.Contains(body, "qc_things_total 0") {
		t.Errorf("declared counter rendered with a value:\n%s", body)
	}

	// A real observation still renders, and keeps the declared help.
	r.Inc("qc_things_total", Labels{"kind": "x"}, "ignored because already declared")
	body = render(t, r)
	if !strings.Contains(body, `qc_things_total{kind="x"} 1`) {
		t.Errorf("observed series missing:\n%s", body)
	}
	if !strings.Contains(body, "# HELP qc_things_total Things counted.") {
		t.Errorf("declared help was replaced by the call-site help:\n%s", body)
	}
}
