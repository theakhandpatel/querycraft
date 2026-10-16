# Result-contract fixtures

The language-neutral fixture suite from `docs/technical-design.md` §6.3. The
result-contract evaluator exists twice, JavaScript (`web/src/api/compare.ts`,
M1) and Go (`backend/internal/eval`, M3), and both are judged against **these
files**. A new comparison rule is added here first, then implemented in both
languages.

```
contracts/fixtures/
  row-order-exact-permutation.json   one file per case
  null-not-zero.json
  tolerance-exact-boundary.json
  ...
```

## Layout

**One JSON file per case**, at the root of this directory. The file name is the
case name. There is no index file: every `*.json` here is loaded by both
harnesses, so adding a case is adding a file.

## Format

Every file is a single object with exactly three required keys and one optional
key:

```json
{
  "note": "Optional human-readable intent.",
  "contract": {
    "columns": ["name", "price"],
    "rows": [["Widget", 10.0]],
    "column_order": "exact",
    "row_order": "ignored",
    "duplicates": "preserved",
    "numeric_tolerance": 0.25
  },
  "actual_result": {
    "status": "succeeded",
    "columns": ["price", "name"],
    "rows": [[10.0, "Widget"]]
  },
  "expected_outcome": { "passed": true, "codes": [] }
}
```

### `contract`

The reference result plus the §6.1 comparison rules. `columns` + `rows` are the
reference output (`ExpectedResult`); the remaining four fields are the
`ResultContract`:

| Key | Values | Default | Meaning |
| --- | --- | --- | --- |
| `columns` | string array | `[]` | Expected column names. |
| `rows` | array of rows | `[]` | Expected values; compared per §6.1. |
| `column_order` | `exact` \| `ignored` | `ignored` | `exact` also checks position. |
| `row_order` | `exact` \| `ignored` | `ignored` | `exact` also checks row order. |
| `duplicates` | `preserved` \| `ignored` | `preserved` | `ignored` compares distinct rows. |
| `numeric_tolerance` | number ≥ 0 | `0` | Absolute tolerance for numeric comparison. |

Unknown or empty enum values fall back to the default, exactly as
`Contract.normalized()` / `normalizeContract()` do. Every case here sets the
fields explicitly so the intent is visible.

### `actual_result`

The result being graded. `status` is `succeeded` or `failed` and defaults to
`succeeded`; `columns` and `rows` are the raw result. A `failed` result is an
execution failure (§6.1 rule 1): it never passes and emits **no** comparison
codes.

### `expected_outcome`

The verdict both evaluators must produce:

```json
{ "passed": false, "codes": ["ROW_COUNT_MISMATCH", "VALUE_MISMATCH"] }
```

- `passed` is the overall verdict.
- `codes` is the exact **set** of feedback codes the comparator emits for the
  case, order-independent and duplicate-free. It is empty when the case passes
  and empty for an execution failure. A code that passes is never listed.

## Code vocabulary

The comparison codes, from `contracts/evaluation.schema.json`:

| Code | Cause |
| --- | --- |
| `COLUMN_MISMATCH` | Names differ, are missing, extra, renamed or mis-cased. |
| `COLUMN_ORDER_MISMATCH` | The right names in the wrong order under `column_order: exact`. |
| `ROW_COUNT_MISMATCH` | A different number of rows (or distinct rows). |
| `VALUE_MISMATCH` | A row or value does not match, or the rows are reordered. |
| `ROW_ORDER_MISMATCH` | `row_order: exact` and the rows are a permutation. |

A pure reorder under `row_order: exact` emits both `VALUE_MISMATCH` and
`ROW_ORDER_MISMATCH`: the positional values differ *and* the order is wrong.

## Adding a case

1. Write the fixture file here first; set `expected_outcome` from the §6.1
   rules, not from what the code currently does.
2. Run both harnesses (`make audit-fixtures` and `go test ./internal/eval/...`).
3. If Go and JavaScript disagree, the §6.1 rules decide which is right; fix the
   mirror in `web/src/api/compare.ts`. A rule is never implemented in one
   language only.

The two harnesses read these same files and share no expected values: the only
thing either one hardcodes is the path to this directory.

## Running

```sh
make audit-fixtures                                   # JavaScript (bundles compare.ts)
cd backend && GOTOOLCHAIN=local go test ./internal/eval/...   # Go
make test                                             # typecheck + audits + go test
```
