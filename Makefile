# Querycraft — root Makefile.
#
# One deployable artifact: `make build` compiles the React SPA, copies it into
# the Go gateway's embedded filesystem, and produces
# backend/bin/querycraft — a single binary that serves both the JSON API and
# the SPA on one port.
#
#   make help     list every target
#   make build    single-binary build
#   make verify   build + tests + smoke test
#
# Recipe lines must be indented with tabs.

.DEFAULT_GOAL := help

# Go is not on PATH in every environment; fall back to the standard install.
GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

WEB_DIR     := web
BACKEND_DIR := backend
EMBED_DIR   := $(BACKEND_DIR)/internal/webui/dist
BIN         := $(BACKEND_DIR)/bin/querycraft

# The `verify` smoke test boots the real binary against its own embedded
# PostgreSQL, in a throwaway cluster on its own port, so it never collides with
# a dev gateway that happens to be running.
# Leave empty to auto-pick a free HTTP port; set it (host:port) to pin one.
SMOKE_ADDR    ?=
# Leave empty to auto-pick a free port; set it to pin one.
SMOKE_PG_PORT ?=
SMOKE_PG_DIR  ?= $(BACKEND_DIR)/.data/smoke

# Prebuilt PostgreSQL binaries are cached between runs; reuse the download
# cache but keep the smoke cluster's data and runtime dirs isolated.
PG_BINARIES_DIR ?= $(BACKEND_DIR)/.data/pg/binaries

.PHONY: help install web typecheck check-cgo embed backend build test test-all gotest verify verify-mock serve run dev docs dataset audit-lessons audit-content audit-storage audit-search audit-courses audit-progress audit-export audit-import audit-fixtures audit-evaluate-wire audit-themes audit-metrics audit-metrics-live audit-adversarial audit-adversarial-live audit-load audit-parity audit-ai audit-ai-adapters audit-course-store audit-ai-live audit-perf audit-perf-content audit-perf-seam audit-perf-fixtures audit-dataset-map audit-shell audit-a11y audit-sqlite-errors audit-boot audit-course-gen db-reset clean disk

help: ## List the available targets with a one-line description.
	@echo "Querycraft — make targets"
	@echo
	@awk 'BEGIN{FS="## "} /^[a-zA-Z0-9_-]+:.*## /{name=$$1; sub(/:.*/,"",name); printf "  %-10s %s\n", name, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "GO=$(GO)"
	@echo "BIN=$(BIN)"

install: ## Install frontend dependencies (npm ci, falling back to npm install).
	cd $(WEB_DIR) && (npm ci || npm install)

web: ## Build the frontend into web/dist.
	cd $(WEB_DIR) && npm run build

typecheck: ## Type-check the frontend (tsc --noEmit, strict).
	cd $(WEB_DIR) && npx tsc --noEmit

lint: ## Lint the frontend with oxlint and the vendored anti-slop rules.
	cd $(WEB_DIR) && npm run lint

doctor: ## Report React Doctor diagnostics for web/src. Informational; a non-zero exit means it found issues.
	cd $(WEB_DIR) && npm run doctor

embed: ## Copy web/dist into the gateway's embedded FS (keeps .gitkeep).
	@if [ ! -f $(WEB_DIR)/dist/index.html ]; then \
		echo "embed: $(WEB_DIR)/dist/index.html not found — run 'make web' first" >&2; \
		exit 1; \
	fi
	@mkdir -p $(EMBED_DIR)
	@find $(EMBED_DIR) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	@cp -R $(WEB_DIR)/dist/. $(EMBED_DIR)/
	@touch $(EMBED_DIR)/.gitkeep
	@echo "embed: $(WEB_DIR)/dist -> $(EMBED_DIR)"

backend: ## Compile the gateway to backend/bin/querycraft, stamping BuildVersion from git.
	@mkdir -p $(BACKEND_DIR)/bin
	cd $(BACKEND_DIR) && $(GO) build -trimpath \
		-ldflags "-s -w -X github.com/querycraft/sqlquery/backend/internal/httpapi.BuildVersion=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" \
		-o bin/querycraft ./cmd/api
	@echo "backend: built $(BIN)"

build: web embed backend ## Produce the single deployable binary (web + embed + backend).

test: ## Run the Go test suite, the frontend type-check, the linter and the audit suites (concurrent, up to TEST_JOBS).
	+@$(MAKE) --no-print-directory -O -j$(TEST_JOBS) test-all

# How many checks run at once. Every target under test-all shares no state —
# each audit binds free ports and stages its own files — so the default is the
# suite's normal width. Lower it on a small machine or a loaded CI box.
TEST_JOBS ?= 8

# test-all is the real suite, one target per check, so `make test` can fan it
# out while a single check can still be run by name (make gotest,
# make audit-parity).
test-all: typecheck lint check-cgo gotest audit-lessons audit-content audit-courses audit-storage audit-search audit-progress audit-export audit-import audit-fixtures audit-evaluate-wire audit-themes audit-metrics audit-adversarial audit-parity audit-dataset-map audit-sqlite-errors audit-perf audit-perf-fixtures audit-perf-content audit-perf-seam audit-ai-adapters audit-course-store audit-course-gen audit-shell audit-a11y audit-boot

gotest: ## (test-all) The Go test suite, as one schedulable job.
	cd $(BACKEND_DIR) && $(GO) test ./...

check-cgo: ## Prove the single binary stays cgo-free (CGO_ENABLED=0 build + a cgo dependency scan).
	cd $(BACKEND_DIR) && CGO_ENABLED=0 $(GO) build ./...
	@cd $(BACKEND_DIR) && set -e; \
		cgo_pkgs="$$(CGO_ENABLED=1 $(GO) list -deps -f '{{if and (not .Standard) .CgoFiles}}{{.ImportPath}}: {{.CgoFiles}}{{end}}' ./...)"; \
		cgo_refs="$$(printf '%s\n' "$$cgo_pkgs" | grep . || true)"; \
		if [ -n "$$cgo_refs" ]; then \
			echo "check-cgo: these packages need cgo, but the single binary must stay cgo-free:" >&2; \
			printf '%s\n' "$$cgo_refs" >&2; \
			exit 1; \
		fi
	@cd $(BACKEND_DIR) && if grep -rn --include='*.go' -E '^//(go:build|[[:space:]]+\+build).*\bcgo\b' . | grep -v '!cgo'; then \
		echo "check-cgo: the cgo-gated files above cannot build with CGO_ENABLED=0" >&2; \
		exit 1; \
	fi

verify: build test ## build + test + smoke-test the binary on an isolated embedded PostgreSQL.
	@set -eu; \
	cfg=$$(mktemp); \
	root=$$(mktemp); log=$$(mktemp); jar=$$(mktemp); \
	rm -rf "$(SMOKE_PG_DIR)"; \
	pid=''; \
	cleanup() { if [ -n "$$pid" ]; then kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; fi; rm -f "$$root" "$$log" "$$cfg" "$$jar"; rm -rf "$(SMOKE_PG_DIR)"; }; \
	trap cleanup EXIT INT TERM; \
	addr_override="$(SMOKE_ADDR)"; \
	port_candidates="$(SMOKE_PG_PORT)"; \
	if [ -z "$$port_candidates" ]; then port_candidates=$$(seq 54340 54440); fi; \
	started=0; \
	for port in $$port_candidates; do \
		pid=''; \
		if [ -n "$$addr_override" ]; then addr="$$addr_override"; else \
			addr=''; \
			for candidate in $$(seq 18080 18180); do \
				if ! ss -ltnH "sport = :$$candidate" 2>/dev/null | grep -q .; then addr=127.0.0.1:$$candidate; break; fi; \
			done; \
			if [ -z "$$addr" ]; then echo "verify: no free HTTP port for the smoke server" >&2; exit 1; fi; \
		fi; \
		rm -rf "$(SMOKE_PG_DIR)"; \
		printf '{"addr":"%s","pg_port":%s,"pg_data":"%s","pg_runtime":"%s","pg_binaries":"%s"}\n' \
			"$$addr" "$$port" "$(SMOKE_PG_DIR)/pg/data" "$(SMOKE_PG_DIR)/pg/runtime" "$(PG_BINARIES_DIR)" >"$$cfg"; \
		: >"$$log"; \
		$(BIN) --config "$$cfg" >"$$log" 2>&1 & \
		pid=$$!; \
		echo "verify: starting $(BIN) on $$addr with embedded PostgreSQL on :$$port (pid $$pid)"; \
		up=0; \
		for _ in $$(seq 1 100); do \
			if curl -fsS "http://$$addr/health" >/dev/null 2>&1; then up=1; break; fi; \
			if ! kill -0 "$$pid" 2>/dev/null; then break; fi; \
			sleep 0.25; \
		done; \
		if [ "$$up" = 1 ]; then started=1; break; fi; \
		if grep -qiE 'address already in use|could not bind|in use' "$$log"; then \
			echo "verify: :$$port was taken by another process; trying the next candidate" >&2; \
			kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; pid=''; \
			continue; \
		fi; \
		echo "verify: server did not become ready or exited early" >&2; cat "$$log" >&2; exit 1; \
	done; \
	if [ "$$started" != 1 ]; then echo "verify: no candidate port for the smoke database became ready" >&2; exit 1; fi; \
	health=$$(curl -fsS "http://$$addr/health"); \
	echo "verify: /health -> $$health"; \
	case "$$health" in *'"version":"PostgreSQL'*) ;; \
		*) echo "verify: embedded PostgreSQL did not attach (health shows the build version, not the server version)" >&2; cat "$$log" >&2; exit 1;; esac; \
	qcode=$$(curl -sS -o "$$root" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"sql":"select count(*) as n from orders"}' \
		"http://$$addr/query"); \
	qr=$$(cat "$$root"); \
	echo "verify: /query -> HTTP $$qcode $$qr"; \
	if [ "$$qcode" != "200" ] || ! grep -q '"status":"succeeded"' "$$root"; then \
		echo "verify: /query did not run against PostgreSQL" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	anon=$$(curl -sS -o /dev/null -w '%{http_code}' "http://$$addr/progress/smoke"); \
	echo "verify: anonymous GET /progress -> HTTP $$anon"; \
	if [ "$$anon" != "401" ]; then \
		echo "verify: /progress must require a session, got $$anon" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	admin=$$(curl -sS -o "$$root" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"email":"admin@querycraft.local","password":""}' \
		"http://$$addr/auth/login"); \
	echo "verify: default admin login -> HTTP $$admin"; \
	if [ "$$admin" != "200" ] || ! grep -q '"role":"admin"' "$$root"; then \
		echo "verify: the seeded default administrator could not sign in" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	tok=$$(printf '%s' "$$(cat "$$root")" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); \
	if [ -z "$$tok" ]; then echo "verify: /auth/login returned no token" >&2; cat "$$root" >&2; exit 1; fi; \
	bearer=$$(curl -sS -o "$$root" -w '%{http_code}' -H "Authorization: Bearer $$tok" \
		"http://$$addr/auth/me"); \
	echo "verify: bearer GET /auth/me -> HTTP $$bearer"; \
	if [ "$$bearer" != "200" ] || ! grep -q '"role":"admin"' "$$root"; then \
		echo "verify: a bearer token must still authorize (API-client contract)" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	signup=$$(curl -sS -c "$$jar" -o "$$root" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"email":"smoke@querycraft.local","name":"Smoke","password":"smoke-passphrase"}' \
		"http://$$addr/auth/signup"); \
	echo "verify: learner signup -> HTTP $$signup (cookie jar)"; \
	if [ "$$signup" != "201" ]; then \
		echo "verify: /auth/signup expected 201, got $$signup" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	put=$$(curl -sS -b "$$jar" -o /dev/null -w '%{http_code}' -X PUT -H 'content-type: application/json' \
		-d '{"version":5,"passed":{"smoke.1":true}}' \
		"http://$$addr/progress/smoke"); \
	get=$$(curl -sS -b "$$jar" -o "$$root" -w '%{http_code}' \
		"http://$$addr/progress/smoke"); \
	echo "verify: cookie-scoped progress PUT/GET -> $$put/$$get"; \
	if [ "$$put" != "200" ] || [ "$$get" != "200" ] || ! grep -q '"smoke.1":true' "$$root"; then \
		echo "verify: progress did not round-trip for the cookie session" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	leak=$$(curl -sS -H "Authorization: Bearer $$tok" "http://$$addr/progress/smoke"); \
	if printf '%s' "$$leak" | grep -q '"smoke.1":true'; then \
		echo "verify: progress leaked across owners (admin saw the learner's snapshot)" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	out=$$(curl -sS -b "$$jar" -c "$$jar" -o /dev/null -w '%{http_code}' -X POST \
		"http://$$addr/auth/logout"); \
	echo "verify: cookie logout -> HTTP $$out"; \
	if [ "$$out" != "200" ]; then echo "verify: logout failed" >&2; exit 1; fi; \
	after=$$(curl -sS -b "$$jar" -o /dev/null -w '%{http_code}' \
		"http://$$addr/progress/smoke"); \
	echo "verify: cookie after logout -> HTTP $$after"; \
	if [ "$$after" != "401" ]; then \
		echo "verify: the session cookie still authorized after logout" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	code=$$(curl -sS -o "$$root" -w '%{http_code}' "http://$$addr/"); \
	echo "verify: GET / -> HTTP $$code"; \
	if [ "$$code" != "200" ]; then echo "verify: GET / expected 200, got $$code" >&2; exit 1; fi; \
	if ! grep -qE 'Querycraft|make build' "$$root"; then \
		echo "verify: GET / did not contain the app title or the placeholder" >&2; head -20 "$$root" >&2; exit 1; \
	fi; \
	echo "verify: SPA response OK"; \
	echo "verify: OK (embedded PostgreSQL :$$port, SPA + API + auth on $$addr)"

verify-mock: build test ## Smoke-test the binary with no embedded database (degraded mode).
	@set -eu; \
	log=$$(mktemp); scratch=$$(mktemp -d); \
	addr="$(SMOKE_ADDR)"; \
	if [ -z "$$addr" ]; then \
		for candidate in $$(seq 18080 18180); do \
			if ! ss -ltnH "sport = :$$candidate" 2>/dev/null | grep -q .; then addr=127.0.0.1:$$candidate; break; fi; \
		done; \
	fi; \
	if [ -z "$$addr" ]; then echo "verify-mock: no free HTTP port for the smoke server" >&2; exit 1; fi; \
	cfg=$$(mktemp); \
	printf '{"addr":"%s","no_local_db":true}\n' "$$addr" >"$$cfg"; \
	$(BIN) --config "$$cfg" >"$$log" 2>&1 & \
	pid=$$!; \
	cleanup() { kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; rm -f "$$log" "$$cfg"; rm -rf "$$scratch"; }; \
	trap cleanup EXIT INT TERM; \
	up=0; \
	for _ in $$(seq 1 60); do \
		if curl -fsS "http://$$addr/health" >/dev/null 2>&1; then up=1; break; fi; \
		if ! kill -0 "$$pid" 2>/dev/null; then echo "verify-mock: server exited early" >&2; cat "$$log" >&2; exit 1; fi; \
		sleep 0.1; \
	done; \
	if [ "$$up" != 1 ]; then echo "verify-mock: /health did not become ready" >&2; cat "$$log" >&2; exit 1; fi; \
	echo "verify-mock: /health -> $$(curl -fsS "http://$$addr/health")"; \
	q=$$(curl -sS -o "$$scratch/q.json" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"sql":"select 1"}' "http://$$addr/query"); \
	echo "verify-mock: /query -> HTTP $$q"; \
	if [ "$$q" != "400" ] || ! grep -q 'ENVIRONMENT_UNAVAILABLE' "$$scratch/q.json"; then \
		echo "verify-mock: degraded mode must report ENVIRONMENT_UNAVAILABLE, not invent rows" >&2; cat "$$scratch/q.json" >&2; exit 1; \
	fi; \
	anon=$$(curl -sS -o /dev/null -w '%{http_code}' "http://$$addr/progress/smoke"); \
	if [ "$$anon" != "401" ]; then echo "verify-mock: /progress must still require a session in degraded mode, got $$anon" >&2; exit 1; fi; \
	adm=$$(curl -sS -o "$$scratch/a.json" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"email":"admin@querycraft.local","password":""}' "http://$$addr/auth/login"); \
	echo "verify-mock: in-memory admin login -> HTTP $$adm"; \
	if [ "$$adm" != "200" ] || ! grep -q '"role":"admin"' "$$scratch/a.json"; then \
		echo "verify-mock: the in-memory store must still seed the default admin" >&2; cat "$$scratch/a.json" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	mtok=$$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' "$$scratch/a.json"); \
	mput=$$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H 'content-type: application/json' -H "Authorization: Bearer $$mtok" \
		-d '{"version":5,"passed":{"m":true}}' "http://$$addr/progress/smoke"); \
	mget=$$(curl -sS -H "Authorization: Bearer $$mtok" "http://$$addr/progress/smoke"); \
	echo "verify-mock: in-memory progress PUT -> $$mput"; \
	if [ "$$mput" != "200" ] || ! printf '%s' "$$mget" | grep -q '"m":true'; then \
		echo "verify-mock: in-memory progress did not round-trip" >&2; printf '%s\n' "$$mget" >&2; exit 1; \
	fi; \
	echo "verify-mock: OK (no database, honest degradation, auth still enforced)"

db-reset: ## Delete the dev, smoke and golden PostgreSQL clusters (fresh dataset next boot).
	@if [ -x $(BIN) ]; then $(BIN) db reset; fi
	rm -rf $(BACKEND_DIR)/.data/pg/data
	rm -rf $(BACKEND_DIR)/.data/pg/golden
	rm -rf $(SMOKE_PG_DIR)
	@echo "db-reset: clusters removed; the next boot re-applies the commerce dataset"

serve: ## Run the built binary (warns if you still need to 'make build').
	@if [ ! -x $(BIN) ]; then \
		echo "serve: $(BIN) not found — run 'make build' first" >&2; \
		exit 1; \
	fi
	$(BIN)

run: ## Run the gateway with 'go run' (API only, no embedded SPA).
	cd $(BACKEND_DIR) && $(GO) run ./cmd/api

dev: ## Print how to run both dev servers (Vite :5173 + gateway :8080).
	@echo "Two dev servers, two shells:"
	@echo "  1) cd $(WEB_DIR) && VITE_API_MODE=http VITE_API_BASE=http://localhost:8080 npm run dev   # Vite on :5173"
	@echo "  2) make run                                                                             # gateway on :8080"

dataset: ## Regenerate backend/internal/dataset/schema.sql and seed.sql from the Go generator.
	cd $(BACKEND_DIR) && $(GO) test ./internal/dataset -run 'Test(Schema|Seed)SQLUpToDate' -update
	@echo "dataset: regenerated backend/internal/dataset/{schema,seed}.sql"

audit-courses: ## Run every authored exercise solution through a gateway, on the dialect and dataset the app would use (see docs/course-audit.md).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum-for-courses.cjs src/data/curriculum.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/dialect-for-courses.cjs src/lib/dialect.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/dataset-for-courses.cjs src/lib/dataset.ts >/dev/null
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/course-audit.mjs

audit-lessons: ## Check every lesson has objectives, an example with a result, and an exercise or a reading-only reason (design 14, M2).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum-for-lessons.cjs src/data/curriculum.ts >/dev/null
	node scripts/lesson-audit.mjs

audit-content: ## Check the chapter-content seeder: fingerprint, ensure-not-once, and the rows it writes (M2 step 8).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/content-seed.cjs src/lib/contentSeed.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum-for-content.cjs src/data/curriculum.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/dialect-for-content.cjs src/lib/dialect.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/dataset-for-content.cjs src/lib/dataset.ts >/dev/null
	node scripts/content-audit.mjs

audit-storage: ## Check the offline store layout and split match design 8.1, and that a failed write is typed (M2 step 2).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/idb.cjs src/api/idb.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/offline-bundle.cjs src/lib/offlineBundle.ts >/dev/null
	node scripts/storage-audit.mjs

audit-search: ## Check search ranking, AND semantics, no-fuzzy matching and empty-query behaviour (M2 step 5).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/search.cjs src/lib/search.ts >/dev/null
	node scripts/search-audit.mjs

audit-progress: ## Check the progress model: measured time only, and v4 snapshots upgrade instead of being dropped.
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress-for-progress.cjs src/lib/progress.ts >/dev/null
	node scripts/progress-audit.mjs

audit-export: ## Check the JSON export document: versioned, complete, credential-free (design 8.3).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/export-bundle-for-export.cjs src/lib/exportBundle.ts >/dev/null
	node scripts/export-audit.mjs

audit-import: ## Check import validation, the merge policy and the export/import round trip (M2 step 7).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/import-bundle.cjs src/lib/importBundle.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/export-bundle-for-import.cjs src/lib/exportBundle.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress-for-import.cjs src/lib/progress.ts >/dev/null
	node scripts/import-audit.mjs

audit-fixtures: ## Check the language-neutral result-contract fixtures against the JS evaluator (the Go half is `go test ./internal/eval/`, run by `make test`).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/fixtures.cjs src/api/compare.ts >/dev/null
	node scripts/fixtures-audit.mjs

audit-evaluate-wire: ## Check the HTTP grading wire: the web sends `result_contract` and `reference_solution`, and the Go Exercise field is tagged `result_contract` (item 17a).
	node scripts/evaluate-wire-audit.mjs

audit-themes: ## Every shipped theme clears 4.5:1 on its key text pairs.
	node scripts/theme-contrast.mjs

audit-metrics: ## Check /metrics and /diagnostics on an isolated gateway with no database (design 13.1, M3).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress-for-metrics.cjs src/lib/progress.ts >/dev/null
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/metrics-audit.mjs

audit-metrics-live: ## Same checks against a running gateway (QC_GATEWAY, default http://localhost:18077), plus timeout and row-cap outcomes.
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress-for-metrics.cjs src/lib/progress.ts >/dev/null
	QC_GATEWAY=$${QC_GATEWAY:-http://localhost:18077} node scripts/metrics-audit.mjs

audit-adversarial: ## Run the ten design 13.3 adversarial cases against a freshly booted gateway (M3 step 3).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/adversarial-audit.mjs

audit-adversarial-live: ## Same ten cases against a gateway you already run (QC_GATEWAY, default http://localhost:18077).
	QC_GATEWAY=$${QC_GATEWAY:-http://localhost:18077} node scripts/adversarial-audit.mjs

audit-load: ## Drive concurrent POST /query load and cross-check client latency against the gateway's own /metrics (M3 step 4).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/load-test.mjs

audit-load-live: ## Same load against a gateway you already run (QC_GATEWAY, default http://localhost:18077).
	QC_GATEWAY=$${QC_GATEWAY:-http://localhost:18077} node scripts/load-test.mjs

audit-ai-adapters: ## Check the frontend AI seam (both adapters, no gateway needed) against contracts/ai.schema.json (M4 step 6).
	@set -e; trap 'rm -f /tmp/qc-ai.cjs /tmp/qc-deadline.cjs' EXIT INT TERM; cd $(WEB_DIR) && npm run audit-ai

audit-course-store: ## Check the M8 stored-course seam: a stored record parses whole (or is dropped), a building course is readable, a failed read refuses instead of reporting none, and the record's measured rows are the rows the lesson page shows.
	@set -e; trap 'rm -f /tmp/qc-ai-course-store.cjs /tmp/qc-draft.cjs /tmp/qc-runresume.cjs /tmp/qc-course-warmup.cjs' EXIT INT TERM; cd $(WEB_DIR) && npm run audit-course-store

audit-ai: ## Run the M4 exit gate against a freshly booted gateway with the deterministic provider (design 11, 14).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/ai-audit.mjs

audit-ai-live: ## Drive the M4 pipeline with a real OpenAI-compatible model (set AI_BASE_URL / AI_MODEL / AI_KEY_FILE).
	node scripts/ai-live-audit.mjs

audit-shell: ## Check the app shell's two layout rules: the clipping container is a containing block, and the 64px rail still names its controls (see the UI pass in progress.md).
	node scripts/shell-audit.mjs

audit-a11y: ## Run axe-core (WCAG 2.x A/AA) over the built SPA's real routes; fail on critical or serious violations (docs/accessibility.md).
	cd $(WEB_DIR) && npm run build
	node scripts/a11y-audit.mjs

audit-boot: ## Drive the in-browser engine's boot with a worker that never answers: it must give up, take the page engine, say so, and run the dataset -- and its election id must be per dataset and per page load (see the UI pass in progress.md).
	node scripts/boot-audit.mjs

audit-course-gen: ## Run the M8 exit gate against a freshly booted gateway: the deterministic provider writes a course chapter by chapter and it activates; a stub provider proves the streaming read (a building course readable mid-run), a chapter that cannot be repaired is dropped while the rest of the course survives, and a run in which nothing verifies leaves no row behind (design 11, 14).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/course-gen-audit.mjs

audit-perf: ## Run the M5 exit gate: plan capture, structural grading and reproducibility on pinned fixtures.
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/perf-audit.mjs
	cd $(WEB_DIR) && npm run audit-perf

audit-perf-fixtures: ## Check every plan fixture's summary is the walk of its own plan, and that its verdict is the pinned one.
	node scripts/perf-fixture-normalize.mjs --check

audit-perf-content: ## Check the perf course against the fixture it describes: every authored row, and every exercise contract against its own solution (M5).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum-for-perf-content.cjs src/data/curriculum.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/plan-contract-for-perf-content.cjs src/api/planContract.ts >/dev/null
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/perf-content-audit.mjs

audit-perf-seam: ## Drive the dataset seam through the shipping browser adapter: a perf chapter routes to the perf fixture, its authored rows come back from it, its plans are graded against it, and a perf write cannot touch commerce (M5).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum-for-perf-seam.cjs src/data/curriculum.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/plan-contract-for-perf-seam.cjs src/api/planContract.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/dataset-for-seam.cjs src/lib/dataset.ts >/dev/null
	node scripts/perf-seam-audit.mjs

audit-dataset-map: ## Check the SQLite dataset mapping against both schemas, every mismatch carrying its reason (M6 step 3).
	node scripts/dataset-map-audit.mjs

audit-sqlite-errors: ## Check the SQLite dialect profile against contracts/dialect.schema.json: closed codes, capability notes, value encoding, affinity, and the browser reader's own framing/classification (M6 step 3).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/sqlite-framing.cjs src/api/sqliteFraming.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/sqlite-profile.cjs src/api/sqliteProfile.ts >/dev/null
	node scripts/sqlite-error-codes.mjs

audit-parity: ## Prove the same lesson SQL returns the same rows on both adapters, for PostgreSQL (PGlite vs the gateway) and for SQLite (browser wasm vs the gateway) (M3/M6 exit gate).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/adapter-parity-audit.mjs
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/adapter-parity-audit.mjs --dialect sqlite

docs: ## Print the Swagger UI and OpenAPI URLs.
	@echo "Swagger UI: http://localhost:8080/docs"
	@echo "OpenAPI:    http://localhost:8080/openapi.json"

clean: ## Remove web/dist, the binary, and embedded assets (keeps .gitkeep).
	rm -rf $(WEB_DIR)/dist $(BACKEND_DIR)/bin
	@mkdir -p $(EMBED_DIR)
	@find $(EMBED_DIR) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	@touch $(EMBED_DIR)/.gitkeep
	@echo "clean: removed build outputs"

disk: ## Report free space and the disk footprint this harness leaves behind (read-only).
	@set -u; tmp="$${TMPDIR:-/tmp}"; \
	echo "Querycraft — disk usage (read-only)"; echo; \
	echo "free space:"; \
	df -h "$$PWD" "$$tmp" 2>/dev/null | awk 'NR==1{printf "  %-22s %6s %6s %6s %5s\n","filesystem","size","used","avail","use%"; next} !seen[$$1]++{printf "  %-22s %6s %6s %6s %5s\n",$$1,$$2,$$3,$$4,$$5}'; \
	echo; \
	echo "repo cache ($(BACKEND_DIR)/.data):"; \
	if [ -d "$(BACKEND_DIR)/.data" ]; then \
		echo "  total   $$(du -sh "$(BACKEND_DIR)/.data" | cut -f1)"; \
		du -sh "$(BACKEND_DIR)"/.data/* 2>/dev/null | sort -rh | head -8 | awk '{printf "  %7s  %s\n",$$1,$$2}'; \
	else echo "  (none)"; fi; \
	echo; \
	echo "harness temp footprint in $$tmp:"; \
	hits=$$(find "$$tmp" -maxdepth 1 \( -name 'qc-*' -o -name 'embedded_postgres_log*' -o -name '*.cjs' \) 2>/dev/null); \
	if [ -z "$$hits" ]; then echo "  clean — no qc-*, embedded_postgres_log* or esbuild *.cjs entries"; \
	else \
		count=$$(printf '%s\n' "$$hits" | wc -l | tr -d ' '); \
		bytes=$$(printf '%s\n' "$$hits" | tr '\n' '\0' | xargs -0 -r du -scb 2>/dev/null | tail -1 | cut -f1); \
		echo "  $$count entries, $$(numfmt --to=iec "$$bytes" 2>/dev/null || echo "$$bytes bytes")"; \
		echo "  largest offenders:"; \
		printf '%s\n' "$$hits" | tr '\n' '\0' | xargs -0 -r du -sh 2>/dev/null | sort -rh | head -8 | awk '{printf "    %7s  %s\n",$$1,$$2}'; \
	fi
