# Documentation index

Start with the architecture, then follow the question you have.

| Question | Document |
| --- | --- |
| How do the parts fit together, and why? | [`architecture.md`](architecture.md) |
| How do I build, test, and extend it? | [`development.md`](development.md) |
| How do I run it on a server, and what should I back up? | [`self-hosting.md`](self-hosting.md) |
| What does the product do, and what is unfinished? | [`features.md`](features.md) |
| What is left to do, in order? | [`open-items.md`](open-items.md) |
| What is the original design, and which decisions are open? | [`technical-design.md`](technical-design.md) |
| Why was a decision made? | [`adr/`](adr/) |
| What are the API endpoints and the configuration keys? | [`../backend/README.md`](../backend/README.md) |
| What has shipped, and what is open? | [`../progress.md`](../progress.md) |

## Data and content

| Document | Contents |
| --- | --- |
| [`database-layout.md`](database-layout.md) | Cluster, database, and schema nesting. The learner role. The metadata store and the practice store. |
| [`sqlite-dialect.md`](sqlite-dialect.md) | The second dialect. What SQLite does differently, and how the app teaches it. |
| [`content-authoring.md`](content-authoring.md) | How to write a chapter. Markdown that the app seeds into `content_docs`. |
| [`export-format.md`](export-format.md) | The versioned JSON export document. No credentials. |

## Evidence

| Document | What it records |
| --- | --- |
| [`accessibility.md`](accessibility.md) | What the keyboard check covers, and what it does not. |
| [`adapter-parity.md`](adapter-parity.md) | The same SQL on each adapter, compared row by row. |
| [`adversarial-suite.md`](adversarial-suite.md) | The design 13.3 cases, and what is not protected. |
| [`load-test.md`](load-test.md) | Concurrency and latency, with the limits of those numbers. |
| [`course-audit.md`](course-audit.md) | Every authored solution, run and graded against itself. |

## Design notes and plans

| Document | Contents |
| --- | --- |
| [`outline-approval-design.md`](outline-approval-design.md) | The approval step between the outline and the chapters. |
| `m2-implementation-plan.md` to `m8-implementation-plan.md` | The plan of record for each milestone, as written. |

## Images

`assets/screens/` holds the screenshots. `assets/walkthrough.*` holds the
recorded walkthrough. The gallery is in [`../README.md`](../README.md).
