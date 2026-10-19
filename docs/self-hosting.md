# Self-hosting Querycraft

Querycraft ships as one binary. It serves the app, the API, and its own
documentation. When you do not point it at a database, it also starts its own
PostgreSQL.

This page covers the first start, configuration, backups, and the operational
details you need on a server. The schema layout is in
[`database-layout.md`](database-layout.md).

## Quick start

```sh
make build
./backend/bin/querycraft
```

The gateway listens on `http://127.0.0.1:8080`. On the first start it downloads
PostgreSQL into `.data/pg/binaries`, creates a cluster under `.data/pg/data`, and
applies the commerce dataset. Later starts reuse that cluster and are immediate.

The paths are relative to the directory you start the binary from. `make serve`
runs from the repository root. `go run ./cmd/api` runs from `backend/`.

Sign in as `admin@querycraft.local` with an empty password. The empty password
works only on a loopback address. Set `admin_password` before you listen on
anything else.

`querycraft serve` names the default command. `querycraft db reset` removes the
local cluster so the next start seeds it again.

## Where settings come from

Settings resolve in this order:

1. The shipped defaults.
2. The config file: the file named by `--config`, or `querycraft.json` in the
   working directory when no name is given.
3. The `--addr` flag on `serve`.

A key that a layer does not name keeps the value from the layer before it. The
[manage screen](features.md) can save a different AI provider in the app
database. That saved setting survives a restart and takes precedence over the
config file for the live provider.

The keys you are most likely to set:

| Key | Default | Use |
| --- | --- | --- |
| `addr` | `127.0.0.1:8080` | Listen address. Keep it on loopback unless you set an admin password. |
| `database_url` | empty | Learner execution DSN. Empty starts the local server. |
| `admin_database_url` | empty | Metadata DSN. Empty falls back to `database_url`. |
| `learner_database_url` | empty | Restricted-role DSN. Empty derives from `database_url`. |
| `no_local_db` | `false` | Never start the local server. |
| `pg_data`, `pg_runtime`, `pg_binaries` | `.data/pg/...` | Local cluster directories. |
| `pg_port`, `pg_user`, `pg_password`, `pg_database` | `54329`, `querycraft`, `querycraft`, `querycraft` | Local server settings. |
| `metadata` | empty (PostgreSQL, or memory with no DSN) | `sqlite` stores accounts and progress in a file. |
| `metadata_path` | derived from `pg_data` | The SQLite metadata file. |
| `sqlite_data` | derived from `pg_data` | Root for per-account SQLite files and the shared `commerce.db`. |
| `admin_email`, `admin_name`, `admin_password` | seeded administrator | The first account. |
| `ai_provider` | `fake` | `fake`, `pi-ai`, `openai-compatible`, or `off`. |
| `ai_model`, `ai_key`, `ai_base_url`, `ai_timeout_ms` | | Provider settings. |
| `max_rows`, `max_bytes`, `timeout_ms`, `max_concurrent` | engine defaults | Request caps. |
| `login_max_failures`, `login_window_ms` | 5, 15 minutes | Login throttling. |

`backend/internal/config/config.go` owns the full catalogue, and
[`../backend/README.md`](../backend/README.md) lists every key.

## Use your own PostgreSQL

Set `database_url` and the gateway stops starting its own server. It creates the
`app` schema and its tables on boot, and it creates or repairs the restricted
learner role.

An external database also needs the learner-visible `dataset` schema. Apply it
once to an empty database:

```sh
psql "$DATABASE_URL" -f backend/internal/dataset/schema.sql
psql "$DATABASE_URL" -f backend/internal/dataset/seed.sql
```

The gateway grants the learner role access to `dataset` on its next start. If you
copy an existing Querycraft database, you can skip the scripts.

`admin_database_url` can name the same server as `database_url`. The two settings
are separate so that the restricted execution role can reach `dataset` alone.

## SQLite metadata

Set `metadata` to `sqlite` to store accounts, sessions, progress, and events in
one file. The file is `metadata_path`, derived from `pg_data` when unset. Learner
queries still use the execution database.

This mode suits a small single-user install. The metadata store also falls back
to an in-memory SQLite database when no database is configured at all, so the
gateway still starts.

## AI providers

AI is optional. The eight built-in courses work without it. Generation routes
answer `503 AI_UNAVAILABLE` when the provider is off.

- `fake` is the default. It is deterministic, uses no network, and powers the
  audits.
- `pi-ai` reaches any catalog provider. Set `ai_model` to a `vendor/model` ref,
  for example `anthropic/claude-sonnet-4-5`, and set `ai_key`.
- `openai-compatible` reaches any OpenAI-shaped endpoint. Set `ai_base_url`,
  `ai_model`, and `ai_key`.
- `off` disables generation.

The key never travels back in a response body. The manage screen shows the
provider, the model, and whether a key is stored. Protect the config file
accordingly.

## Day two

- Health: `GET /health`, `GET /metrics`, and the session-gated `GET /diagnostics`.
- Backup: stop the gateway, then copy `.data/pg/data` and the SQLite metadata
  file when you use it. For an external server, use `pg_dump`.
- Reset: `querycraft db reset` removes the local cluster. `make db-reset` also
  removes the smoke cluster and the test golden cluster.
- Disk: `make disk` reports the footprint the build and test tools leave behind.
- Logs: one access line per request on stderr. The line names the operation, the
  route, the status, the duration, the request id, and the stable error code.

## Upgrades

Replace the binary. The `app` schema migrates on boot with additive changes such
as `ALTER TABLE ... ADD COLUMN`, so an existing cluster keeps its data. The
dataset seed applies once. A cluster from an older layout may be refused by name.
In that case, `querycraft db reset` recreates the local cluster. Back up first.

## Run without PostgreSQL

Set `no_local_db` to `true` and leave `database_url` empty to start with no
database at all. Accounts, sessions, and progress live in memory for the life of
the process. Queries answer `ENVIRONMENT_UNAVAILABLE`. Use this for smoke tests,
not for learners. `make verify-mock` runs that mode.

## Ports and files

| What | Default |
| --- | --- |
| HTTP | `127.0.0.1:8080` |
| PostgreSQL local server | `127.0.0.1:54329` |
| Cluster | `.data/pg/data` |
| Binary cache | `.data/pg/binaries` |
| Test golden cluster | `.data/pg/golden` |
| SQLite metadata | derived from `pg_data` |

## Security notes

- The default listen address is loopback, which is what allows the seeded
  administrator to keep an empty password. Set `admin_password` before you bind
  anywhere else.
- Anonymous visitors run statements on the shared dataset through a restricted
  role that cannot write. A signed-in learner gets a workspace of their own.
- Login attempts are throttled per address and account. The defaults and the
  bounds live in `backend/internal/auth/login_throttle.go`.
- The SPA holds its session in the `qc_session` cookie. The cookie is
  `HttpOnly`, `SameSite=Lax`, `Path=/`, and `Secure` when the request arrived
  over TLS. A cookie-authenticated mutating request must be same-origin or it is
  refused with `403 CSRF_REFUSED`. Serve the SPA same-origin. A cross-origin page
  cannot use the cookie.
- API clients authenticate with the bearer token that signup and login return.
  The token has a TTL. Logout revokes it, and a revoked token answers `401`.
