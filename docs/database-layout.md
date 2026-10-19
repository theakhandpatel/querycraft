# Database layout

Querycraft nests PostgreSQL objects so one server can host many isolated
environments. The layout has three levels: cluster, database, and schema.

## The three levels

| Level | What it is | Querycraft's use |
| --- | --- | --- |
| Cluster | One `postgres` server process, one data directory, one set of roles. | The local server the gateway starts (`pg_data`), or the `db` service in `docker-compose.yml`. One cluster is enough for every environment. |
| Database | A catalog inside the cluster. A DSN selects it. Databases cannot read each other's tables. | One database per environment. `querycraft` for development and preview, `querycraft_test` for the gated integration tests. `deploy/postgres/initdb/01-databases.sql` creates both. |
| Schema | A namespace inside a database, addressed as `schema.table`. | Two schemas per database: `dataset` and `app`. |

```text
cluster  (one postgres process)
├── database querycraft            ← development and preview
│   ├── schema dataset             learner-visible commerce tables
│   │   ├── customers  products  orders  order_items  dataset_meta
│   │   └── the only schema on a learner query's search_path
│   └── schema app                 gateway metadata, never learner-visible
│       ├── users  sessions         accounts and bearer-token sessions
│       ├── progress  events        owner-scoped learner data
│       ├── challenges  courses  patches   generated artifacts
│       ├── runs                    generation transcripts
│       ├── user_settings  settings
│       └── (PGlite also keeps content_docs and content_meta here)
└── database querycraft_test       ← gated integration tests
    ├── schema dataset
    └── schema app
```

The browser database has a smaller `app` schema. It holds `content_docs` and
`content_meta` for chapter content that a learner can query. Lessons read the
bundled markdown directly, so opening a chapter does not wait for this database.
It does not hold accounts or progress, because those live in IndexedDB in
browser mode.

## Why the dataset is isolated

`dataset` is the only schema a learner statement can address unqualified. Every
anonymous learner query runs inside a read-only transaction whose session sets:

```sql
SET LOCAL search_path = dataset
```

So `SELECT count(*) FROM orders` resolves to `dataset.orders`. An unqualified
`SELECT * FROM progress` fails with `42P01` (relation does not exist), because
gateway metadata is not on the path.

That separation provides four things:

- Least privilege. A restricted execution role needs `USAGE` on `dataset` and
  `SELECT` on its tables. It needs nothing from `app`, so learner SQL cannot read
  or write progress snapshots or events. The gateway's own store connects with a
  separate connection that holds `app`.
- Clean reset. Rebuilding the dataset touches only `dataset`. Metadata in `app`
  survives.
- Per-account isolation. The gateway never trusts a caller-supplied user id. It
  resolves the bearer token or cookie to an account and filters every metadata
  read and write by that id.
- Honest environments. Test and preview databases run the same layout, so an
  environment is a different DSN, not a different code path.

The learner role also omits `public` from its `search_path`. PostgreSQL lets any
role create objects in `public` by default, so leaving it on the path could make
an operator's later object silently reachable from a learner statement.

## Create the layout

On a local cluster, the gateway creates everything. It:

- Runs `backend/internal/dataset/schema.sql`, which starts with
  `CREATE SCHEMA IF NOT EXISTS dataset` and creates every learner table as
  `dataset.<table>`.
- Runs the metadata migration, which starts with
  `CREATE SCHEMA IF NOT EXISTS app` and creates `app.users`, `app.sessions`,
  `app.progress`, `app.events`, and the generated-artifact tables.
- Creates or repairs the restricted learner role and its grants.

Progress and events carry a `user_id` foreign key with `ON DELETE CASCADE`.
`app.users` has a unique index on `lower(email)`, so addresses are unique
regardless of case.

An external PostgreSQL needs the dataset applied once. The gateway does not seed
it, because it cannot tell an empty database from one you meant to keep:

```sh
psql "$DATABASE_URL" -f backend/internal/dataset/schema.sql
psql "$DATABASE_URL" -f backend/internal/dataset/seed.sql
```

The gateway's local server checks `to_regclass('dataset.customers')` to decide
whether the dataset is already applied, so an existing data directory keeps its
rows.

## Point the gateway at a container

`database_url` in `querycraft.json` selects the environment. The gateway creates
the `app` schema on first connect. The `docker-compose.yml` service starts
PostgreSQL 16 with both databases:

```sh
docker compose up -d db

# Development and preview
printf '{"database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable","admin_database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable"}\n' > querycraft.json
psql 'postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable' -f backend/internal/dataset/schema.sql
psql 'postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable' -f backend/internal/dataset/seed.sql
(cd backend && go run ./cmd/api)

# A different environment is a different database on the same server
printf '{"database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft_test?sslmode=disable"}\n' > querycraft.json

# Gated integration test
(cd backend && QC_TEST_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft_test?sslmode=disable' \
  go test ./internal/httpapi -run TestGatewayIntegration -v)
```

With `database_url` unset, the gateway starts its own PostgreSQL and seeds the
same layout into a repository-local data directory (`pg_data`, default
`.data/pg`). Docker is not required for the default development flow.

## Verify the separation

```sh
DSN='postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable'

# Both schemas exist
psql "$DSN" -Atc "select nspname from pg_namespace where nspname in ('dataset','app')"

# The learner tables and the gateway tables
psql "$DSN" -c '\dt dataset.*'
psql "$DSN" -c '\dt app.*'

# An unqualified learner table resolves
curl -fsS -X POST localhost:8080/query -H 'content-type: application/json' \
  -d '{"sql":"SELECT count(*) AS n FROM orders"}'

# Metadata does not resolve
curl -fsS -X POST localhost:8080/query -H 'content-type: application/json' \
  -d '{"sql":"SELECT count(*) FROM progress"}'
```

The second query returns `QUERY_RUNTIME_ERROR`. The learner role has no grant on
`app`, and `app` is not on its search path.
