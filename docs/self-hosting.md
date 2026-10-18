# Self-hosting Querycraft

Querycraft is one binary. It serves the app, the API, and its own documentation.
When you do not point it at a database, it also starts its own PostgreSQL. This
page covers the quick path and the day-two operations. The design notes live in
[`database-layout.md`](database-layout.md); the defaults live in
`backend/internal/config/config.go`.

## Quick start

```sh
make build
./backend/bin/querycraft
```

The gateway listens on `http://127.0.0.1:8080`. The first boot downloads
PostgreSQL once into `backend/.data/pg/binaries`, creates a cluster under
`backend/.data/pg/data`, and applies the commerce dataset. Expect about a minute
for the first boot and about a second afterwards.

Sign in as `admin@querycraft.local` with an empty password. The empty password
is accepted on a loopback address only. Set `admin_password` before you listen
on anything else.

`querycraft serve` says the default command out loud. `querycraft db reset`
removes the local cluster so the next boot re-seeds it.

## Where settings come from

Settings resolve in this order: defaults, then `querycraft.json` in the working
directory, then the file named by `--config`, then the `--addr` flag. The
[`manage`](features.md) screen writes AI settings back to the same file.

The keys you are most likely to set:

| Key | Default | Use |
| --- | --- | --- |
| `addr` | `127.0.0.1:8080` | Listen address. Keep it on loopback unless you set an admin password. |
| `database_url` | empty | Execution DSN. Empty starts the local server. |
| `admin_database_url` | empty | Metadata DSN. Empty falls back to `database_url`. |
| `learner_database_url` | empty | Restricted-role DSN. Empty derives from `database_url`. |
| `no_local_db` | `false` | Never start the local server. |
| `pg_data`, `pg_runtime`, `pg_binaries` | `backend/.data/pg/...` | Local cluster directories. |
| `pg_port`, `pg_user`, `pg_password`, `pg_database` | `54329`, `querycraft`, `querycraft`, `querycraft` | Local server settings. |
| `metadata` | empty (PostgreSQL) | `sqlite` stores accounts and progress in a file. |
| `metadata_path` | derived from `pg_data` | The SQLite metadata file. |
| `admin_email`, `admin_name`, `admin_password` | seeded admin | The first account. |
| `ai_provider` | `fake` | `fake`, `pi-ai`, `openai-compatible`, or an off word. |
| `ai_model`, `ai_key`, `ai_base_url`, `ai_timeout_ms` | | Provider settings. |
| `max_rows`, `max_bytes`, `timeout_ms`, `max_concurrent` | engine defaults | Request caps. |
| `login_max_failures`, `login_window_ms` | 5, 15 minutes | Login throttling. |

`backend/internal/config/config.go` owns the full catalogue. Read it before you
trust a table on a wiki.

## Using your own PostgreSQL

Set `database_url` and the gateway stops starting its own server. The gateway
creates the `app` schema and its tables on boot. The learner-visible `dataset`
schema comes from `schema.sql` and `seed.sql` and applies on the first boot
against an empty database. [`database-layout.md`](database-layout.md) describes
the three levels, the role split, and how to verify the separation.

Keep the two concerns in one server if you like: `admin_database_url` can name
the same server as `database_url`. They are separate settings so a restricted
execution role can reach `dataset` alone.

## SQLite metadata

`metadata: "sqlite"` stores accounts, sessions, progress, and events in one
file (`metadata_path`, derived from `pg_data` when unset). Queries still run on
PostgreSQL, local or external. This mode suits a small single-user install. The
metadata store also degrades to an in-memory SQLite database when no database is
configured at all, so the gateway still starts.

## AI providers

AI is optional. The eight built-in courses work without it. Generation routes
answer `503 AI_UNAVAILABLE` when the provider is off.

- `fake` (default): deterministic, no network. It proves the pipeline and powers
  the audits.
- `pi-ai`: any catalog provider. Set `ai_model` to a `vendor/model` ref, for
  example `anthropic/claude-sonnet-4-5`, and set `ai_key`.
- `openai-compatible`: any OpenAI-shaped endpoint. Set `ai_base_url`,
  `ai_model`, and `ai_key`.

The key never travels back in a response body. The manage screen shows the
provider, the model, and whether a key is stored. Protect the config file
accordingly.

## Day two

- Health: `GET /health`, `GET /metrics`, and the session-gated `GET /diagnostics`.
- Backup: stop the gateway, then copy `backend/.data/pg/data` (or run `pg_dump`
  against your own server) and the SQLite metadata file when you use it.
- Reset: `querycraft db reset` removes the local cluster. `make db-reset` also
  removes the smoke cluster and the test golden cluster.
- Disk: `make disk` reports the footprint this project leaves behind.
- Logs: one access line per request on stderr. The line names the operation, the
  route, the status, the duration, the request id, and the stable error code.

## Upgrades

Replace the binary. The `app` schema migrates on boot with additive changes
(`ALTER TABLE ... ADD COLUMN`), so an existing cluster keeps its data. The
dataset seed applies once. A cluster from a much older layout may be refused by
name; `querycraft db reset` recreates it. Back up first anyway.

## Running without PostgreSQL

`no_local_db: true` with no `database_url` starts the gateway with no database
at all. Accounts, sessions, and progress live in memory for the process, and
queries answer `ENVIRONMENT_UNAVAILABLE`. Use it for smoke tests, not for
learners. `make verify-mock` runs that mode.

## Ports and files

| What | Default |
| --- | --- |
| HTTP | `127.0.0.1:8080` |
| PostgreSQL (local server) | `127.0.0.1:54329` |
| Cluster | `backend/.data/pg/data` |
| Binary cache | `backend/.data/pg/binaries` |
| Test golden cluster | `backend/.data/pg/golden` |
| SQLite metadata | derived from `pg_data` |

## Security notes

- The default listen address is loopback, which is what allows the seeded
  administrator to keep an empty password. Set `admin_password` before you bind
  anywhere else.
- Anonymous visitors run statements on the shared dataset through a restricted
  role that cannot write. A signed-in learner gets a workspace of their own.
- Login attempts are throttled per address and account. The defaults and the
  bounds live in `backend/internal/httpapi/login_throttle.go`.
- Session tokens are bearer tokens with a TTL. Logout revokes the token, and a
  revoked token answers `401`.
