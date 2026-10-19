# Documentation

Querycraft teaches SQL by having you write it against a real database. These
guides cover installation, the architecture, content authoring, and operations.

## Get started

| Topic | Document |
| --- | --- |
| Install and run locally | [../README.md](../README.md) |
| Build, test, and extend | [development.md](development.md) |
| Run on a server, configure, and back up | [self-hosting.md](self-hosting.md) |
| Feature reference | [features.md](features.md) |
| Build and publish the browser sample | [static-deploy.md](static-deploy.md) |

## Understand the system

| Topic | Document |
| --- | --- |
| Components, request path, and data model | [architecture.md](architecture.md) |
| Cluster, database, and schema layout | [database-layout.md](database-layout.md) |
| The SQLite dialect and how the app teaches it | [sqlite-dialect.md](sqlite-dialect.md) |
| The same SQL on each adapter, compared row by row | [adapter-parity.md](adapter-parity.md) |

## Author and operate

| Topic | Document |
| --- | --- |
| Write a chapter | [content-authoring.md](content-authoring.md) |
| The versioned export document | [export-format.md](export-format.md) |
| Run every authored solution through a gateway | [course-audit.md](course-audit.md) |
| Concurrency and latency under load | [load-test.md](load-test.md) |
| The adversarial suite and the resource-exhaustion fuzz phase | [adversarial-suite.md](adversarial-suite.md) |

## Decisions

| Topic | Document |
| --- | --- |
| Why the system is built this way | [adr/](adr/) |

## API

| Topic | Document |
| --- | --- |
| Endpoints, configuration keys, and the CLI | [../backend/README.md](../backend/README.md) |

The running gateway also serves OpenAPI 3.1 at `/openapi.json` and Swagger UI at
`/docs`.
