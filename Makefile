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
SMOKE_ADDR    ?= :18080
# Leave empty to auto-pick a free port; set it to pin one.
SMOKE_PG_PORT ?=
SMOKE_PG_DIR  ?= $(BACKEND_DIR)/.data/smoke

# Prebuilt PostgreSQL binaries are cached between runs; reuse the download
# cache but keep the smoke cluster's data and runtime dirs isolated.
PG_BINARIES_DIR ?= $(BACKEND_DIR)/.data/pg/binaries

.PHONY: help install web typecheck embed backend build test verify verify-mock serve run dev docs dataset audit-lessons audit-content audit-storage audit-search audit-courses audit-progress audit-export audit-import audit-fixtures audit-metrics audit-metrics-live audit-adversarial audit-adversarial-live audit-load audit-parity db-reset clean

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

test: typecheck audit-lessons audit-content audit-storage audit-search audit-progress audit-export audit-import audit-fixtures audit-metrics audit-adversarial audit-parity ## Run the Go test suite, the frontend type-check and the audit suites.
	cd $(BACKEND_DIR) && $(GO) test ./...

verify: build test ## build + test + smoke-test the binary on an isolated embedded PostgreSQL.
	@set -eu; \
	root=$$(mktemp); log=$$(mktemp); \
	rm -rf "$(SMOKE_PG_DIR)"; \
	port="$(SMOKE_PG_PORT)"; \
	if [ -z "$$port" ]; then \
		for candidate in $$(seq 54340 54440); do \
			if ! ss -ltnH "sport = :$$candidate" 2>/dev/null | grep -q .; then port=$$candidate; break; fi; \
		done; \
	fi; \
	if [ -z "$$port" ]; then echo "verify: no free port for the smoke database" >&2; exit 1; fi; \
	QC_ADDR=$(SMOKE_ADDR) \
	QC_PG_PORT="$$port" \
	QC_PG_DATA="$(SMOKE_PG_DIR)/pg/data" \
	QC_PG_RUNTIME="$(SMOKE_PG_DIR)/pg/runtime" \
	QC_PG_BINARIES="$(PG_BINARIES_DIR)" \
	$(BIN) >"$$log" 2>&1 & \
	pid=$$!; \
	cleanup() { kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; rm -f "$$root" "$$log"; }; \
	trap cleanup EXIT INT TERM; \
	echo "verify: starting $(BIN) on $(SMOKE_ADDR) with embedded PostgreSQL on :$$port (pid $$pid)"; \
	up=0; \
	for _ in $$(seq 1 100); do \
		if curl -fsS "http://localhost$(SMOKE_ADDR)/health" >/dev/null 2>&1; then up=1; break; fi; \
		if ! kill -0 "$$pid" 2>/dev/null; then echo "verify: server exited before becoming ready" >&2; cat "$$log" >&2; exit 1; fi; \
		sleep 0.25; \
	done; \
	if [ "$$up" != 1 ]; then echo "verify: /health did not become ready" >&2; cat "$$log" >&2; exit 1; fi; \
	health=$$(curl -fsS "http://localhost$(SMOKE_ADDR)/health"); \
	echo "verify: /health -> $$health"; \
	case "$$health" in *'"version":"PostgreSQL'*) ;; \
		*) echo "verify: embedded PostgreSQL did not attach (health shows the build version, not the server version)" >&2; cat "$$log" >&2; exit 1;; esac; \
	qcode=$$(curl -sS -o "$$root" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"sql":"select count(*) as n from orders"}' \
		"http://localhost$(SMOKE_ADDR)/query"); \
	qr=$$(cat "$$root"); \
	echo "verify: /query -> HTTP $$qcode $$qr"; \
	if [ "$$qcode" != "200" ] || ! grep -q '"status":"succeeded"' "$$root"; then \
		echo "verify: /query did not run against PostgreSQL" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	anon=$$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	echo "verify: anonymous GET /progress -> HTTP $$anon"; \
	if [ "$$anon" != "401" ]; then \
		echo "verify: /progress must require a session, got $$anon" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	admin=$$(curl -sS -o "$$root" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"email":"admin@querycraft.local","password":""}' \
		"http://localhost$(SMOKE_ADDR)/auth/login"); \
	echo "verify: default admin login -> HTTP $$admin"; \
	if [ "$$admin" != "200" ] || ! grep -q '"role":"admin"' "$$root"; then \
		echo "verify: the seeded default administrator could not sign in" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	tok=$$(printf '%s' "$$(cat "$$root")" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); \
	if [ -z "$$tok" ]; then echo "verify: /auth/login returned no token" >&2; cat "$$root" >&2; exit 1; fi; \
	signup=$$(curl -sS -o "$$root" -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"email":"smoke@querycraft.local","name":"Smoke","password":"smoke-passphrase"}' \
		"http://localhost$(SMOKE_ADDR)/auth/signup"); \
	echo "verify: learner signup -> HTTP $$signup"; \
	if [ "$$signup" != "201" ]; then \
		echo "verify: /auth/signup expected 201, got $$signup" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	learner=$$(printf '%s' "$$(cat "$$root")" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); \
	if [ -z "$$learner" ]; then echo "verify: /auth/signup returned no token" >&2; cat "$$root" >&2; exit 1; fi; \
	put=$$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H 'content-type: application/json' \
		-H "Authorization: Bearer $$learner" \
		-d '{"version":5,"passed":{"smoke.1":true}}' \
		"http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	get=$$(curl -sS -o "$$root" -w '%{http_code}' -H "Authorization: Bearer $$learner" \
		"http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	echo "verify: scoped progress PUT/GET -> $$put/$$get"; \
	if [ "$$put" != "200" ] || [ "$$get" != "200" ] || ! grep -q '"smoke.1":true' "$$root"; then \
		echo "verify: progress did not round-trip for the signed-in learner" >&2; cat "$$root" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	leak=$$(curl -sS -H "Authorization: Bearer $$tok" "http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	if printf '%s' "$$leak" | grep -q '"smoke.1":true'; then \
		echo "verify: progress leaked across owners (admin saw the learner's snapshot)" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	out=$$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $$learner" \
		"http://localhost$(SMOKE_ADDR)/auth/logout"); \
	echo "verify: logout -> HTTP $$out"; \
	if [ "$$out" != "200" ]; then echo "verify: logout failed" >&2; exit 1; fi; \
	after=$$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $$learner" \
		"http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	echo "verify: revoked token -> HTTP $$after"; \
	if [ "$$after" != "401" ]; then \
		echo "verify: a logged-out token still worked" >&2; cat "$$log" >&2; exit 1; \
	fi; \
	code=$$(curl -sS -o "$$root" -w '%{http_code}' "http://localhost$(SMOKE_ADDR)/"); \
	echo "verify: GET / -> HTTP $$code"; \
	if [ "$$code" != "200" ]; then echo "verify: GET / expected 200, got $$code" >&2; exit 1; fi; \
	if ! grep -qE 'Querycraft|make build' "$$root"; then \
		echo "verify: GET / did not contain the app title or the placeholder" >&2; head -20 "$$root" >&2; exit 1; \
	fi; \
	echo "verify: SPA response OK"; \
	echo "verify: OK (embedded PostgreSQL :$$port, SPA + API + auth on $(SMOKE_ADDR))"

verify-mock: build test ## Smoke-test the binary with no embedded database (degraded mode).
	@set -eu; \
	log=$$(mktemp); \
	QC_ADDR=$(SMOKE_ADDR) QC_NO_LOCAL_DB=1 $(BIN) >"$$log" 2>&1 & \
	pid=$$!; \
	cleanup() { kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; rm -f "$$log"; }; \
	trap cleanup EXIT INT TERM; \
	up=0; \
	for _ in $$(seq 1 60); do \
		if curl -fsS "http://localhost$(SMOKE_ADDR)/health" >/dev/null 2>&1; then up=1; break; fi; \
		if ! kill -0 "$$pid" 2>/dev/null; then echo "verify-mock: server exited early" >&2; cat "$$log" >&2; exit 1; fi; \
		sleep 0.1; \
	done; \
	if [ "$$up" != 1 ]; then echo "verify-mock: /health did not become ready" >&2; cat "$$log" >&2; exit 1; fi; \
	echo "verify-mock: /health -> $$(curl -fsS "http://localhost$(SMOKE_ADDR)/health")"; \
	q=$$(curl -sS -o /tmp/qc-mock-q.json -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"sql":"select 1"}' "http://localhost$(SMOKE_ADDR)/query"); \
	echo "verify-mock: /query -> HTTP $$q"; \
	if [ "$$q" != "400" ] || ! grep -q 'ENVIRONMENT_UNAVAILABLE' /tmp/qc-mock-q.json; then \
		echo "verify-mock: degraded mode must report ENVIRONMENT_UNAVAILABLE, not invent rows" >&2; cat /tmp/qc-mock-q.json >&2; exit 1; \
	fi; \
	anon=$$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	if [ "$$anon" != "401" ]; then echo "verify-mock: /progress must still require a session in degraded mode, got $$anon" >&2; exit 1; fi; \
	adm=$$(curl -sS -o /tmp/qc-mock-a.json -w '%{http_code}' -X POST -H 'content-type: application/json' \
		-d '{"email":"admin@querycraft.local","password":""}' "http://localhost$(SMOKE_ADDR)/auth/login"); \
	echo "verify-mock: in-memory admin login -> HTTP $$adm"; \
	if [ "$$adm" != "200" ] || ! grep -q '"role":"admin"' /tmp/qc-mock-a.json; then \
		echo "verify-mock: the in-memory store must still seed the default admin" >&2; cat /tmp/qc-mock-a.json >&2; cat "$$log" >&2; exit 1; \
	fi; \
	mtok=$$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' /tmp/qc-mock-a.json); \
	mput=$$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H 'content-type: application/json' -H "Authorization: Bearer $$mtok" \
		-d '{"version":5,"passed":{"m":true}}' "http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	mget=$$(curl -sS -H "Authorization: Bearer $$mtok" "http://localhost$(SMOKE_ADDR)/progress/smoke"); \
	echo "verify-mock: in-memory progress PUT -> $$mput"; \
	if [ "$$mput" != "200" ] || ! printf '%s' "$$mget" | grep -q '"m":true'; then \
		echo "verify-mock: in-memory progress did not round-trip" >&2; printf '%s\n' "$$mget" >&2; exit 1; \
	fi; \
	rm -f /tmp/qc-mock-q.json /tmp/qc-mock-a.json; \
	echo "verify-mock: OK (no database, honest degradation, auth still enforced)"

db-reset: ## Delete the dev and smoke PostgreSQL clusters (fresh dataset next boot).
	rm -rf $(SMOKE_PG_DIR) $(BACKEND_DIR)/.data/pg/data
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

audit-courses: ## Run every authored exercise solution through a running gateway (see docs/course-audit.md).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum.cjs src/data/curriculum.ts >/dev/null
	node scripts/course-audit.mjs

audit-lessons: ## Check every lesson has objectives, an example with a result, and an exercise or a reading-only reason (design 14, M2).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum.cjs src/data/curriculum.ts >/dev/null
	node scripts/lesson-audit.mjs

audit-content: ## Check the chapter-content seeder: fingerprint, ensure-not-once, and the rows it writes (M2 step 8).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/content-seed.cjs src/lib/contentSeed.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/curriculum-for-content.cjs src/data/curriculum.ts >/dev/null
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
		--outfile=/tmp/progress.cjs src/lib/progress.ts >/dev/null
	node scripts/progress-audit.mjs

audit-export: ## Check the JSON export document: versioned, complete, credential-free (design 8.3).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/export-bundle.cjs src/lib/exportBundle.ts >/dev/null
	node scripts/export-audit.mjs

audit-import: ## Check import validation, the merge policy and the export/import round trip (M2 step 7).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/import-bundle.cjs src/lib/importBundle.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/export-bundle.cjs src/lib/exportBundle.ts >/dev/null
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress.cjs src/lib/progress.ts >/dev/null
	node scripts/import-audit.mjs

audit-fixtures: ## Check the language-neutral result-contract fixtures against the JS evaluator (Go half: go test ./internal/eval/...).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/fixtures.cjs src/api/compare.ts >/dev/null
	node scripts/fixtures-audit.mjs

audit-metrics: ## Check /metrics and /diagnostics on an isolated gateway with no database (design 13.1, M3).
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress.cjs src/lib/progress.ts >/dev/null
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/metrics-audit.mjs

audit-metrics-live: ## Same checks against a running gateway (QC_GATEWAY, default http://localhost:18077), plus timeout and row-cap outcomes.
	cd $(WEB_DIR) && ./node_modules/.bin/esbuild --bundle --format=cjs --platform=node \
		--outfile=/tmp/progress.cjs src/lib/progress.ts >/dev/null
	QC_GATEWAY=$${QC_GATEWAY:-http://localhost:18077} node scripts/metrics-audit.mjs

audit-adversarial: ## Run the ten design 13.3 adversarial cases against a freshly booted gateway (M3 step 3).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/adversarial-audit.mjs

audit-adversarial-live: ## Same ten cases against a gateway you already run (QC_GATEWAY, default http://localhost:18077).
	QC_GATEWAY=$${QC_GATEWAY:-http://localhost:18077} node scripts/adversarial-audit.mjs

audit-load: ## Drive concurrent POST /query load and cross-check client latency against the gateway's own /metrics (M3 step 4).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/load-test.mjs

audit-load-live: ## Same load against a gateway you already run (QC_GATEWAY, default http://localhost:18077).
	QC_GATEWAY=$${QC_GATEWAY:-http://localhost:18077} node scripts/load-test.mjs

audit-parity: ## Prove the same lesson SQL returns the same rows on PGlite (Adapter A) and the Go gateway (Adapter B) (M3 exit gate).
	QC_GO=$(GO) QC_BACKEND=$(BACKEND_DIR) node scripts/adapter-parity-audit.mjs

docs: ## Print the Swagger UI and OpenAPI URLs.
	@echo "Swagger UI: http://localhost:8080/docs"
	@echo "OpenAPI:    http://localhost:8080/openapi.json"

clean: ## Remove web/dist, the binary, and embedded assets (keeps .gitkeep).
	rm -rf $(WEB_DIR)/dist $(BACKEND_DIR)/bin
	@mkdir -p $(EMBED_DIR)
	@find $(EMBED_DIR) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	@touch $(EMBED_DIR)/.gitkeep
	@echo "clean: removed build outputs"
