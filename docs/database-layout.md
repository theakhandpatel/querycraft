# Database layout: cluster, database, schema

Version 0.1. How Querycraft nests PostgreSQL objects so one server can host many
isolated environments without containers multiplying.

## The three levels

| Level | What it is | Querycraft's use |
| --- | --- | --- |
| Cluster | One `postgres` server process, one data directory, one set of roles. | The local server the gateway starts (`QC_PG_DATA`), or the `db` service in `docker-compose.yml`. One cluster is plenty for every environment. |
| Database | A catalog inside the cluster. Connection strings (DSNs) select it. Databases cannot read each other's tables. | **One database per environment**: `querycraft` for dev/preview, `querycraft_test` for the gated integration tests. `deploy/postgres/initdb/01-databases.sql` creates both. |
| Schema | A namespace inside a database. Same server, same connection, addressable as `schema.table`. | **Two schemas per database**: `dataset` and `app`. |

```
cluster  (one postgres process)
├── database querycraft            ← dev / preview (QC_DATABASE_URL)
│   ├── schema dataset             learner-visible commerce tables
│   │   ├── customers  products  orders  order_items  dataset_meta
│   │   └── (also the only schema on a learner query's search_path)
│   └── schema app                 gateway metadata, never learner-visible
│       ├── users  sessions         accounts and bearer-token sessions
│       ├── progress  events         owner-scoped, FK to users(id)
│       └── content_docs            (in-browser PGlite mode)
└── database querycraft_test       ← gated integration tests
    ├── schema dataset
    └── schema app
```

## Why the dataset is isolated

`dataset` is the only schema a learner statement can address unqualified. Every
learner query runs inside a read-only transaction whose session sets:

```sql
SET LOCAL search_path = dataset, public
```

So `SELECT count(*) FROM orders` resolves to `dataset.orders`, while an
unqualified `SELECT * FROM progress` fails with `42P01` (relation does not
exist) — gateway metadata is not reachable by accident or by probing.

That separation buys three things:

- **Least privilege.** A restricted execution role only needs
  `GRANT USAGE ON SCHEMA dataset` and `GRANT SELECT ON ALL TABLES IN SCHEMA
  dataset`. It needs nothing from `app`, so progress snapshots and events can
  never be read or written by learner SQL. The gateway's own store connects
  with a separate connection that does hold `app`.
- **Clean reset.** Rebuilding the dataset (`DROP`/`CREATE` in
  `backend/internal/dataset/schema.sql`) touches only `dataset`; learner
  metadata in `app` survives.
- **Per-account isolation.** The gateway never trusts a caller-supplied user
  id: it resolves the bearer token to an account and filters every metadata
  read and write by that id, so `SELECT` on `app.progress` for a signed-in
  learner can only ever return their own rows.
- **Honest environments.** Test and preview databases run the same layout, so
  an environment is just a different DSN, not a different code path.

`public` stays on the search path only as PostgreSQL's conventional fallback;
the reserved `public` schema is unused by Querycraft's own objects.

## Creating the layout from zero

Nothing needs manual schema setup. On a fresh database:

- `backend/internal/dataset/schema.sql` starts with
  `CREATE SCHEMA IF NOT EXISTS dataset;` and creates every learner table
  qualified as `dataset.<table>`.
- The gateway's metadata migration starts with
  `CREATE SCHEMA IF NOT EXISTS app;` and creates `app.users`, `app.sessions`,
  `app.progress` and `app.events`. Progress and events carry a `user_id`
  foreign key with `ON DELETE CASCADE`, and `app.users` has a unique index on
  `lower(email)` so addresses are case-insensitively unique.
- PGlite mode applies the same `dataset` script and creates `app.content_docs`.

The gateway's local server (`backend/internal/localdb`) checks
`to_regclass('dataset.customers')` to decide whether the dataset is already
applied, so an existing data directory keeps its rows.

## Pointing the gateway at a container

`QC_DATABASE_URL` selects the environment; the gateway creates the schemas on
first connect. Use the `docker-compose.yml` service:

```sh
docker compose up -d db

# dev / preview
export QC_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable'
export QC_ADMIN_DATABASE_URL="$QC_DATABASE_URL"   # optional: persist progress/events
(cd backend && go run ./cmd/api)

# a different environment is a different database on the same server
export QC_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft_test?sslmode=disable'

# gated integration test
(cd backend && QC_TEST_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft_test?sslmode=disable' \
  go test ./internal/httpapi -run TestGatewayIntegration -v)
```

With `QC_DATABASE_URL` unset the gateway starts its own PostgreSQL and seeds the
same layout into a repo-local data directory (`QC_PG_DATA`, default
`.data/pg`); with `QC_DATABASE_URL` set it uses the server as-is. Docker is not
required for the default dev flow.

## Verifying the separation

```sh
# both schemas exist
psql "$QC_DATABASE_URL" -Atc "select nspname from pg_namespace where nspname in ('dataset','app')"
# dataset   -> the learner tables
psql "$QC_DATABASE_URL" -c '\dt dataset.*'
# app       -> gateway metadata
psql "$QC_DATABASE_URL" -c '\dt app.*'

# unqualified learner table resolves
curl -fsS -X POST localhost:8080/query -H 'content-type: application/json' \
  -d '{"sql":"SELECT count(*) AS n FROM orders"}'
# unqualified metadata does not (search_path separation)
curl -fsS -X POST localhost:8080/query -H 'content-type: application/json' \
  -d '{"sql":"SELECT count(*) FROM progress"}'
```
