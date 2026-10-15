package dataset_test

// D11 spike: measure what it actually costs to give every generated challenge
// its own PostgreSQL database, created as a physical copy of one sealed
// template database.
//
// The test boots its own embedded PostgreSQL on a free port with a data
// directory under t.TempDir(), reuses the repository's cached PostgreSQL
// binaries so it needs no network, and removes everything it creates. It never
// touches a gateway a developer may be running: different port, different
// cluster.
//
// Run it alone with:
//
//	export PATH=$PATH:/usr/local/go/bin
//	cd backend && go test ./internal/dataset -run TestTemplateEnvironmentSpike -v -count=1
//
// The numbers it produces are pasted (with the date) into
// docs/adr/0002-challenge-environments.md. Set QC_SPIKE_OUT=<path> to also write
// the raw measurements as JSON.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/localdb"
)

// spikePassword is the password the learner role gets inside the spike cluster.
// It is a throwaway cluster under t.TempDir(); it is never written to the tree.
const spikePassword = "spike-learner-password"

// copyRuns is how many template copies are timed individually (including the
// first, cold one).
const copyRuns = 7

// burstRuns is how many copies are timed as one burst, to show the cost of a
// class of learners arriving at once.
const burstRuns = 8

// dropRuns is how many drops are timed, so the reported cost is a distribution
// and not one sample that may have landed on a checkpoint.
const dropRuns = 4

func TestTemplateEnvironmentSpike(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a PostgreSQL server; skipped under -short")
	}

	root := t.TempDir()
	port := freePort(t)
	binaries := cachedBinaries()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := localdb.Config{
		DataPath:     filepath.Join(root, "pg", "data"),
		RuntimePath:  filepath.Join(root, "pg", "runtime"),
		BinariesPath: binaries,
		TempDir:      filepath.Join(root, "tmp"),
		Port:         port,
		User:         "querycraft",
		Password:     "querycraft",
		Database:     "querycraft",
	}

	srv, err := localdb.Start(ctx, quietLogger(), cfg)
	if err != nil {
		if binaries == "" {
			t.Skipf("no cached PostgreSQL binaries and no server could be started: %v", err)
		}
		t.Fatalf("boot embedded PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	admin, err := pgx.Connect(ctx, srv.DSN)
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	var report strings.Builder
	line := func(format string, args ...any) {
		fmt.Fprintf(&report, format+"\n", args...)
	}

	// --- server facts -----------------------------------------------------
	var version string
	if err := admin.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatalf("read server_version: %v", err)
	}
	facts, err := dataset.ServerConnectionFacts(ctx, admin)
	if err != nil {
		t.Fatalf("read connection facts: %v", err)
	}
	line("server: PostgreSQL %s on :%d", version, port)
	line("connection slots: max_connections=%d, superuser_reserved_connections=%d, usable by the learner role=%d",
		facts.MaxConnections, facts.SuperuserReserved, facts.LearnableConnections)

	// --- build the template ----------------------------------------------
	template := dataset.TemplateDatabaseName
	templateCreate, err := timeCall(func() error { return dataset.CreateDatabase(ctx, admin, template) })
	if err != nil {
		t.Fatalf("create the template database: %v", err)
	}
	loadTime, err := timeCall(func() error { return dataset.LoadIntoTemplate(ctx, admin, template, spikePassword) })
	if err != nil {
		t.Fatalf("load the dataset into the template: %v", err)
	}
	line("template build: CREATE DATABASE %s TEMPLATE template0 = %s; schema+seed+learner grants = %s",
		template, round(templateCreate), round(loadTime))

	// --- PostgreSQL's rule, measured rather than assumed ------------------
	// A database cannot be copied while another session is attached to it.
	attached, err := dataset.ConnectToDatabase(ctx, admin, template)
	if err != nil {
		t.Fatalf("attach to the template: %v", err)
	}
	refusal := dataset.CreateDatabaseFromTemplate(ctx, admin, dataset.EnvironmentDatabaseName("spike_refused"), template)
	if refusal == nil {
		t.Errorf("PostgreSQL copied a database with a session attached; the seal step would be unnecessary")
		_ = dataset.DropDatabase(ctx, admin, dataset.EnvironmentDatabaseName("spike_refused"))
	}
	line("refusal with one session attached (allow_connections=true): %v", refusal)
	if err := attached.Close(ctx); err != nil {
		t.Fatalf("detach from the template: %v", err)
	}

	sealTime, err := timeCall(func() error { return dataset.SealTemplate(ctx, admin, template) })
	if err != nil {
		t.Fatalf("seal the template: %v", err)
	}
	line("seal (IS_TEMPLATE true, ALLOW_CONNECTIONS false) = %s", round(sealTime))

	// The template is now unreachable as a session database, which is exactly
	// the guarantee that makes copying it legal at any time.
	if _, err := dataset.ConnectToDatabase(ctx, admin, template); err == nil {
		t.Errorf("a session could still attach to the sealed template")
	} else {
		line("session to the sealed template: refused (%s)", firstLine(err))
	}

	// --- copy cost --------------------------------------------------------
	copies := make([]time.Duration, 0, copyRuns)
	names := make([]string, 0, copyRuns+burstRuns)
	for i := 0; i < copyRuns; i++ {
		name := dataset.EnvironmentDatabaseName(fmt.Sprintf("spike_copy_%02d", i))
		took, err := timeCall(func() error { return dataset.CreateDatabaseFromTemplate(ctx, admin, name, template) })
		if err != nil {
			t.Fatalf("copy the template (%d): %v", i, err)
		}
		copies = append(copies, took)
		names = append(names, name)
	}
	lo, mid, hi := summarize(copies)
	line("template copies (n=%d, includes the first/cold one): min=%s median=%s max=%s; first=%s rest(n=%d) min=%s median=%s max=%s",
		len(copies), round(lo), round(mid), round(hi), round(copies[0]),
		len(copies)-1, round(minOf(copies[1:])), round(medianOf(copies[1:])), round(maxOf(copies[1:])))

	burstStart := time.Now()
	for i := 0; i < burstRuns; i++ {
		name := dataset.EnvironmentDatabaseName(fmt.Sprintf("spike_burst_%02d", i))
		if err := dataset.CreateDatabaseFromTemplate(ctx, admin, name, template); err != nil {
			t.Fatalf("burst copy (%d): %v", i, err)
		}
		names = append(names, name)
	}
	burst := time.Since(burstStart)
	line("burst of %d copies: total=%s, mean=%s per environment", burstRuns, round(burst), round(burst/time.Duration(burstRuns)))

	// --- disk cost --------------------------------------------------------
	templateSize, err := dataset.EnvironmentDatabaseSize(ctx, admin, template)
	if err != nil {
		t.Fatalf("template size: %v", err)
	}
	envSize, err := dataset.EnvironmentDatabaseSize(ctx, admin, names[0])
	if err != nil {
		t.Fatalf("environment size: %v", err)
	}
	line("disk: template=%s, one environment=%s (%d environments = %s)",
		humanBytes(templateSize), humanBytes(envSize), len(names), humanBytes(int64(len(names))*envSize))

	// --- does the copy carry the data and the grants? ---------------------
	templateCounts := tableCounts(t, ctx, admin, template)
	envCounts := tableCounts(t, ctx, admin, names[0])
	if fmt.Sprint(templateCounts) != fmt.Sprint(envCounts) {
		t.Errorf("environment row counts %v differ from the template %v", envCounts, templateCounts)
	}
	line("row counts in a copy: %v (equal to the template: %v)", envCounts, true)

	learner, err := dataset.ConnectAsLearner(ctx, admin, names[0], spikePassword)
	if err != nil {
		t.Fatalf("the learner role cannot connect to a fresh environment: %v", err)
	}
	defer func() { _ = learner.Close(context.WithoutCancel(ctx)) }()

	var searchPath string
	if err := learner.QueryRow(ctx, "SHOW search_path").Scan(&searchPath); err != nil {
		t.Fatalf("read the learner search_path: %v", err)
	}
	line("learner in a copy: search_path=%q (role default, cluster-wide, applies without re-granting)", searchPath)

	var privileges struct {
		selectGranted bool
		writeGranted  bool
	}
	if err := learner.QueryRow(ctx, "SELECT has_table_privilege('dataset.orders','SELECT')").Scan(&privileges.selectGranted); err != nil {
		t.Fatalf("has_table_privilege(SELECT): %v", err)
	}
	if err := learner.QueryRow(ctx, "SELECT has_table_privilege('dataset.orders','INSERT')").Scan(&privileges.writeGranted); err != nil {
		t.Fatalf("has_table_privilege(INSERT): %v", err)
	}
	if !privileges.selectGranted {
		t.Error("the copy did not carry the learner's SELECT grant; every environment would need a re-grant pass")
	}
	if privileges.writeGranted {
		t.Error("the learner role can write to the dataset in a fresh environment")
	}
	line("grants carried by the copy: SELECT on dataset=%v, INSERT on dataset=%v", privileges.selectGranted, privileges.writeGranted)

	// A realistic analytic query, unqualified, exactly as a learner writes it.
	rows, err := learner.Query(ctx, `
		SELECT p.category, count(*) AS items, round(sum(oi.quantity * oi.unit_price), 2) AS revenue
		FROM order_items oi
		JOIN products p ON p.id = oi.product_id
		GROUP BY p.category
		ORDER BY revenue DESC`)
	if err != nil {
		t.Fatalf("analytic query in a fresh environment: %v", err)
	}
	analytic := 0
	for rows.Next() {
		analytic++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read analytic query: %v", err)
	}
	if analytic == 0 {
		t.Error("the analytic query returned no rows")
	}
	line("analytic query (join + group by + aggregate) in a copy: %d rows, unqualified names resolved via search_path", analytic)

	if _, err := learner.Exec(ctx, "DELETE FROM products"); err == nil {
		t.Error("the learner deleted rows in a fresh environment")
	}

	// The learner role is cluster-wide, and PUBLIC holds CONNECT on every
	// database by default, so the role can also attach to the gateway's own
	// metadata database. Measure it rather than assume either way: it is not a
	// leak of another learner's rows (progress lives in `app`, which is denied)
	// but it is a surface M4 should close explicitly.
	metaConn, metaErr := dataset.ConnectAsLearner(ctx, admin, cfg.Database, spikePassword)
	if metaErr != nil {
		line("learner session to the metadata database %q: refused (%s)", cfg.Database, firstLine(metaErr))
	} else {
		var connectable bool
		if err := metaConn.QueryRow(ctx, "SELECT has_database_privilege(current_user, current_database(), 'CONNECT')").Scan(&connectable); err != nil {
			t.Fatalf("has_database_privilege(CONNECT): %v", err)
		}
		var datasetVisible bool
		if err := metaConn.QueryRow(ctx, "SELECT to_regclass('dataset.orders') IS NOT NULL").Scan(&datasetVisible); err != nil {
			t.Fatalf("check dataset.orders in the metadata database: %v", err)
		}
		line("learner session to the metadata database %q: allowed (CONNECT=%v, it also holds a dataset schema visible to the learner=%v; CONNECT is the PUBLIC default and is not revoked)",
			cfg.Database, connectable, datasetVisible)
		_ = metaConn.Close(context.WithoutCancel(ctx))
	}

	// --- drop cost --------------------------------------------------------
	// Several drops, not one: the first drop in a cluster can be an order of
	// magnitude slower than the rest when PostgreSQL decides to checkpoint.
	idleDrops := make([]time.Duration, 0, dropRuns)
	for i := 0; i < dropRuns; i++ {
		name := names[len(names)-1-i]
		took, err := timeCall(func() error { return dataset.DropDatabase(ctx, admin, name) })
		if err != nil {
			t.Fatalf("drop an environment: %v", err)
		}
		idleDrops = append(idleDrops, took)
	}
	names = names[:len(names)-dropRuns]
	dropLo, dropMid, dropHi := summarize(idleDrops)
	line("drop (n=%d): min=%s median=%s max=%s; first=%s",
		len(idleDrops), round(dropLo), round(dropMid), round(dropHi), round(idleDrops[0]))

	pinned := dataset.EnvironmentDatabaseName("spike_pinned")
	if err := dataset.CreateDatabaseFromTemplate(ctx, admin, pinned, template); err != nil {
		t.Fatalf("create the pinned environment: %v", err)
	}
	pinnedLearner, err := dataset.ConnectAsLearner(ctx, admin, pinned, spikePassword)
	if err != nil {
		t.Fatalf("attach a learner session to the pinned environment: %v", err)
	}
	dropForced, err := timeCall(func() error { return dataset.DropDatabase(ctx, admin, pinned) })
	if err != nil {
		t.Fatalf("drop with an open learner session (FORCE): %v", err)
	}
	_ = pinnedLearner.Close(context.WithoutCancel(ctx))
	line("drop with an open learner session (FORCE terminates it, n=1)=%s", round(dropForced))

	resetTime, err := timeCall(func() error { return dataset.ResetEnvironment(ctx, admin, names[0], template) })
	if err != nil {
		t.Fatalf("reset an environment: %v", err)
	}
	line("reset (DROP + CREATE from template) = %s", round(resetTime))

	// --- connection-slot ceiling -----------------------------------------
	ceilingEnv := dataset.EnvironmentDatabaseName("spike_ceiling")
	if err := dataset.CreateDatabaseFromTemplate(ctx, admin, ceilingEnv, template); err != nil {
		t.Fatalf("create the ceiling environment: %v", err)
	}
	measuredCeiling, _ := learnerCeiling(ctx, admin, ceilingEnv)
	if measuredCeiling > 0 && measuredCeiling < facts.LearnableConnections {
		line("learner sessions before the server refused: %d (lower than the %d the arithmetic predicts; recorded, not asserted)",
			measuredCeiling, facts.LearnableConnections)
	} else {
		line("learner sessions before the server refused: %d (max_connections minus reserved = %d)",
			measuredCeiling, facts.LearnableConnections)
	}
	pool := fmt.Sprintf("pgxpool MaxConns = max(4, NumCPU) = %d, MinConns = 1 (NumCPU here = %d)", maxInt(4, runtime.NumCPU()), runtime.NumCPU())
	line("cost in connection slots: %s; at rest a template environment holds none, one per active environment while a pool is open", pool)
	if facts.LearnableConnections > 0 {
		line("concurrency bound with the gateway pool: about %d environments if every one holds its full pool, %d if every one is idle",
			facts.LearnableConnections/maxInt(4, runtime.NumCPU()), facts.LearnableConnections)
	}

	// --- reaper ----------------------------------------------------------
	keep := []string{names[0], names[1], names[2]}
	dropped, err := dataset.ReapEnvironmentDatabases(ctx, admin, keep)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	remaining, err := dataset.ListEnvironmentDatabases(ctx, admin)
	if err != nil {
		t.Fatalf("list environments: %v", err)
	}
	sort.Strings(remaining)
	if len(remaining) != len(keep) {
		t.Errorf("after the reap %d environments remain, want the %d passed to keep: %v", len(remaining), len(keep), remaining)
	}
	line("reap(keep=%d): dropped %d unreferenced environments, %d remain", len(keep), len(dropped), len(remaining))

	// --- what an orphan costs across a restart ---------------------------
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop the server before the restart: %v", err)
	}
	restartStart := time.Now()
	srv2, err := localdb.Start(ctx, quietLogger(), cfg)
	if err != nil {
		t.Fatalf("restart the server: %v", err)
	}
	restartTook := time.Since(restartStart)
	t.Cleanup(func() { _ = srv2.Stop() })

	admin2, err := pgx.Connect(ctx, srv2.DSN)
	if err != nil {
		t.Fatalf("reconnect as admin after the restart: %v", err)
	}
	defer func() { _ = admin2.Close(context.WithoutCancel(ctx)) }()

	afterRestart, err := dataset.ListEnvironmentDatabases(ctx, admin2)
	if err != nil {
		t.Fatalf("list environments after the restart: %v", err)
	}
	sizeAfter := int64(0)
	for _, name := range afterRestart {
		size, err := dataset.EnvironmentDatabaseSize(ctx, admin2, name)
		if err != nil {
			t.Fatalf("size of %s after the restart: %v", name, err)
		}
		sizeAfter += size
	}
	if len(afterRestart) != len(remaining) {
		t.Errorf("after a restart %d environments exist, want the %d that existed before; something reaped them, or they were lost", len(afterRestart), len(remaining))
	}
	line("restart: server back in %s; %d environment databases survived, holding %s; nothing reaps them on boot",
		round(restartTook), len(afterRestart), humanBytes(sizeAfter))

	// --- optional machine-readable copy ----------------------------------
	if out := os.Getenv("QC_SPIKE_OUT"); out != "" {
		payload := map[string]any{
			"measured_at":                   time.Now().Format(time.RFC3339),
			"postgres_version":              version,
			"port":                          port,
			"max_connections":               facts.MaxConnections,
			"superuser_reserved":            facts.SuperuserReserved,
			"learnable_connections":         facts.LearnableConnections,
			"learner_sessions_measured":     measuredCeiling,
			"template_create_ms":            templateCreate.Milliseconds(),
			"template_load_ms":              loadTime.Milliseconds(),
			"seal_ms":                       sealTime.Milliseconds(),
			"copy_count":                    len(copies),
			"copy_first_ms":                 copies[0].Milliseconds(),
			"copy_min_ms":                   lo.Milliseconds(),
			"copy_median_ms":                mid.Milliseconds(),
			"copy_max_ms":                   hi.Milliseconds(),
			"burst_count":                   burstRuns,
			"burst_total_ms":                burst.Milliseconds(),
			"template_bytes":                templateSize,
			"environment_bytes":             envSize,
			"drop_idle_min_ms":              dropLo.Milliseconds(),
			"drop_idle_median_ms":           dropMid.Milliseconds(),
			"drop_idle_max_ms":              dropHi.Milliseconds(),
			"drop_idle_first_ms":            idleDrops[0].Milliseconds(),
			"drop_forced_ms":                dropForced.Milliseconds(),
			"reset_ms":                      resetTime.Milliseconds(),
			"restart_ms":                    restartTook.Milliseconds(),
			"environments_surviving_boot":   len(afterRestart),
			"num_cpu":                       runtime.NumCPU(),
			"refusal_with_session_attached": fmt.Sprint(refusal),
		}
		blob, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			t.Fatalf("marshal the spike report: %v", err)
		}
		if err := os.WriteFile(out, append(blob, '\n'), 0o644); err != nil {
			t.Fatalf("write the spike report: %v", err)
		}
	}

	t.Logf("\n--- D11 template-environment spike (measured on this machine) ---\n%s", report.String())
}

// quietLogger discards the embedded server's log; the spike reports its own
// summary and a noisy server log would drown it.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// cachedBinaries finds an already-extracted PostgreSQL under the repository's
// .data directory so the spike needs no network. An empty result lets
// embedded-postgres fall back to its own download cache.
func cachedBinaries() string {
	if v := os.Getenv("QC_PG_BINARIES"); v != "" {
		return v
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, ".data", "pg", "binaries")
		if _, err := os.Stat(filepath.Join(candidate, "bin", "pg_ctl")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// freePort picks an unused TCP port, so the spike never collides with a gateway.
func freePort(t *testing.T) uint32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the free port: %v", err)
	}
	return uint32(port)
}

func timeCall(fn func() error) (time.Duration, error) {
	start := time.Now()
	err := fn()
	return time.Since(start), err
}

func summarize(samples []time.Duration) (min, median, max time.Duration) {
	return minOf(samples), medianOf(samples), maxOf(samples)
}

func minOf(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	best := samples[0]
	for _, s := range samples[1:] {
		if s < best {
			best = s
		}
	}
	return best
}

func maxOf(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	best := samples[0]
	for _, s := range samples[1:] {
		if s > best {
			best = s
		}
	}
	return best
}

func medianOf(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	half := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[half]
	}
	return (sorted[half-1] + sorted[half]) / 2
}

func tableCounts(t *testing.T, ctx context.Context, conn *pgx.Conn, database string) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, table := range []string{"customers", "products", "orders", "order_items"} {
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM dataset."+table).Scan(&n); err != nil {
			t.Fatalf("count %s.%s: %v", database, table, err)
		}
		counts[table] = n
	}
	var metaVersion int
	if err := conn.QueryRow(ctx, "SELECT version FROM dataset.dataset_meta").Scan(&metaVersion); err != nil {
		t.Fatalf("read dataset version in %s: %v", database, err)
	}
	counts["dataset_version"] = metaVersion
	return counts
}

// learnerCeiling opens learner sessions to one environment until the server
// refuses, proving the connection budget is real rather than theoretical. A
// best-effort measurement: the caller treats a short count as an observation.
func learnerCeiling(ctx context.Context, admin *pgx.Conn, database string) (int, error) {
	conns := make([]*pgx.Conn, 0, 128)
	defer func() {
		for _, c := range conns {
			_ = c.Close(context.WithoutCancel(ctx))
		}
	}()
	for i := 0; i < 256; i++ {
		connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err := dataset.ConnectAsLearner(connectCtx, admin, database, spikePassword)
		cancel()
		if err != nil {
			return len(conns), nil
		}
		conns = append(conns, conn)
	}
	return len(conns), nil
}

func round(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return d.String()
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}

func firstLine(err error) string {
	if err == nil {
		return ""
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
