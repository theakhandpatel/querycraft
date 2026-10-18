// Package metrics is Querycraft's Adapter B instrumentation: a small,
// dependency-free registry with Prometheus text exposition.
//
// It exists because the adversarial suite (design §13.3) and the load test
// (§13.2) publish numbers, and a number produced by a stopwatch beside a
// running server is a story rather than evidence. Every counter here is
// incremented on the code path that owns the event.
//
// The surface is deliberately small (counter, gauge, histogram) because that is
// the whole of what §13.1 asks for, and a metrics client library would be more
// code than the thing it measures.
package metrics

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Labels is a set of label pairs. An empty value means "this label is absent",
// which keeps a single-series metric free of braces.
type Labels map[string]string

// defaultBuckets are latency buckets in seconds. They straddle the gateway's
// 5 s default deadline so a timeout is visible as a bucket, not only as an
// error counter.
var defaultBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type kind uint8

const (
	kindCounter kind = iota
	kindGauge
	kindHistogram
)

func (k kind) String() string {
	switch k {
	case kindGauge:
		return "gauge"
	case kindHistogram:
		return "histogram"
	default:
		return "counter"
	}
}

type series struct {
	name    string
	labels  Labels
	help    string
	kind    kind
	value   float64   // counter and gauge
	buckets []float64 // histogram upper bounds, ascending, without +Inf
	counts  []uint64  // histogram, one slot per bucket plus +Inf; not cumulative
	sum     float64
	total   uint64
}

// described is the help text and kind of a metric that has been declared but
// may not have been observed yet.
type described struct {
	help string
	kind kind
}

// Registry holds every series the gateway reports. The zero value is not
// usable; call New.
type Registry struct {
	mu       sync.RWMutex
	series   map[string]*series
	declared map[string]described
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{series: map[string]*series{}, declared: map[string]described{}}
}

// DescribeCounter declares a counter without creating a series. A service that
// publishes the shape of its instrumentation before the first event gives a
// scrape (and a diagnostics view) an answer even when nothing has happened yet.
func (r *Registry) DescribeCounter(name, help string) { r.describe(name, help, kindCounter) }

// DescribeGauge declares a gauge.
func (r *Registry) DescribeGauge(name, help string) { r.describe(name, help, kindGauge) }

// DescribeHistogram declares a histogram.
func (r *Registry) DescribeHistogram(name, help string) { r.describe(name, help, kindHistogram) }

func (r *Registry) describe(name, help string, k kind) {
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.declared[name] = described{help: help, kind: k}
}

func seriesKey(name string, l Labels) string {
	keys := make([]string, 0, len(l))
	for k, v := range l {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteByte(0x1f)
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(l[k])
	}
	return b.String()
}

// ensure returns the series, creating it. Callers hold the write lock.
func (r *Registry) ensure(name string, l Labels, help string, k kind, buckets []float64) *series {
	key := seriesKey(name, l)
	if s, ok := r.series[key]; ok {
		return s
	}
	clean := Labels{}
	for kk, vv := range l {
		if vv != "" {
			clean[kk] = vv
		}
	}
	s := &series{name: name, labels: clean, help: help, kind: k}
	if k == kindHistogram {
		s.buckets = append([]float64(nil), buckets...)
		s.counts = make([]uint64, len(buckets)+1)
	}
	r.series[key] = s
	return s
}

// Inc adds one to a counter.
func (r *Registry) Inc(name string, l Labels, help string) { r.add(name, l, 1, help) }

// add adds delta to a counter.
func (r *Registry) add(name string, l Labels, delta float64, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure(name, l, help, kindCounter, nil).value += delta
}

// Set stores a gauge.
func (r *Registry) Set(name string, l Labels, v float64, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensure(name, l, help, kindGauge, nil).value = v
}

// Observe records one value in a histogram. A nil bucket list means
// defaultBuckets.
func (r *Registry) Observe(name string, l Labels, v float64, help string, buckets []float64) {
	if len(buckets) == 0 {
		buckets = defaultBuckets
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.ensure(name, l, help, kindHistogram, buckets)
	s.sum += v
	s.total++
	idx := len(s.buckets)
	for i, ub := range s.buckets {
		if v <= ub {
			idx = i
			break
		}
	}
	s.counts[idx]++
}

// Value reads a counter or gauge. The second result is false if the series does
// not exist, which is different from existing with value zero.
func (r *Registry) Value(name string, l Labels) (float64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.series[seriesKey(name, l)]
	if !ok || s.kind == kindHistogram {
		return 0, false
	}
	return s.value, true
}

// Histogram reads a histogram's total count and sum.
func (r *Registry) Histogram(name string, l Labels) (count uint64, sum float64, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, found := r.series[seriesKey(name, l)]
	if !found || s.kind != kindHistogram {
		return 0, 0, false
	}
	return s.total, s.sum, true
}

// Names lists every metric name, sorted. The diagnostics view uses it to say
// what is being observed without hard-coding the list twice.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]bool{}
	for n := range r.declared {
		seen[n] = true
	}
	for _, s := range r.series {
		seen[s.name] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// WritePrometheus renders the registry in the Prometheus text exposition
// format: one HELP/TYPE pair per metric name, then its series. A declared
// metric with no observations still appears, without samples.
func (r *Registry) WritePrometheus(w io.Writer) error {
	groups, declared := r.render()

	names := make([]string, 0, len(groups)+len(declared))
	seen := map[string]bool{}
	for n := range declared {
		seen[n] = true
		names = append(names, n)
	}
	for n := range groups {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('\n')
		}
		group := groups[name]
		help, k := "", kindCounter
		if d, ok := declared[name]; ok {
			help, k = d.help, d.kind
		} else if len(group) > 0 {
			help, k = group[0].help, group[0].kind
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, k)
		slices.SortFunc(group, func(a, b series) int {
			return strings.Compare(seriesKey(a.name, a.labels), seriesKey(b.name, b.labels))
		})
		for _, s := range group {
			switch s.kind {
			case kindHistogram:
				var cum uint64
				for i, ub := range s.buckets {
					cum += s.counts[i]
					fmt.Fprintf(&b, "%s_bucket%s %d\n", s.name, joinLabels(s.labels, "le", formatFloat(ub)), cum)
				}
				cum += s.counts[len(s.buckets)]
				fmt.Fprintf(&b, "%s_bucket%s %d\n", s.name, joinLabels(s.labels, "le", "+Inf"), cum)
				fmt.Fprintf(&b, "%s_sum%s %s\n", s.name, labelsOnly(s.labels), formatFloat(s.sum))
				fmt.Fprintf(&b, "%s_count%s %d\n", s.name, labelsOnly(s.labels), s.total)
			default:
				fmt.Fprintf(&b, "%s%s %s\n", s.name, labelsOnly(s.labels), formatFloat(s.value))
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// render copies the registry into per-name groups so rendering never races a
// live counter.
func (r *Registry) render() (map[string][]series, map[string]described) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groups := make(map[string][]series, len(r.series))
	for _, s := range r.series {
		c := *s
		c.counts = append([]uint64(nil), s.counts...)
		c.buckets = append([]float64(nil), s.buckets...)
		groups[s.name] = append(groups[s.name], c)
	}
	declared := make(map[string]described, len(r.declared))
	for n, d := range r.declared {
		declared[n] = d
	}
	return groups, declared
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// The replacers are package-level: strings.Replacer is safe for concurrent use,
// and building one per call allocated on every rendered metric.
var (
	helpEscaper  = strings.NewReplacer("\\", "\\\\", "\n", "\\n")
	labelEscaper = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
)

func escapeHelp(s string) string {
	return helpEscaper.Replace(s)
}

func escapeLabel(s string) string {
	return labelEscaper.Replace(s)
}

// labelsOnly renders the label set as `{a="b"}`, or an empty string when the
// set has no labels. It is joinLabels with no extra pair.
func labelsOnly(l Labels) string {
	return joinLabels(l, "", "")
}

// joinLabels renders the label set plus one extra pair, sorted. Passing an
// empty key skips the pair.
func joinLabels(l Labels, extraKey, extraValue string) string {
	pairs := make([]string, 0, len(l)+1)
	for k, v := range l {
		if v != "" {
			pairs = append(pairs, k+"=\""+escapeLabel(v)+"\"")
		}
	}
	if extraKey != "" {
		pairs = append(pairs, extraKey+"=\""+escapeLabel(extraValue)+"\"")
	}
	if len(pairs) == 0 {
		return ""
	}
	sort.Strings(pairs)
	return "{" + strings.Join(pairs, ",") + "}"
}
