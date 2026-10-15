/*
 * Re-derive the `summary` block of every `contracts/fixtures/perf-*.json`.
 *
 * A fixture is a document, not a capture: the plan tree is the evidence, and
 * `summary` is what the app's own walk makes of it (`summarizePlan`, the same
 * walk in Go — backend/internal/engine/explain.go). The two must agree, because
 * the summary is the only thing the graders read. When the walk changes — as it
 * did for the relation a Bitmap Index Scan does not name — the stored summaries
 * are stale, and a hand-authored fixture whose plan never said `Relation Name`
 * in the first place carries a summary that no walk could have produced.
 *
 * Two repairs, in this order:
 *
 *   1. If the walk cannot produce a relation the stored summary claimed at the
 *      same position, the *plan* is what is wrong: the node is given the
 *      `Relation Name` and `Alias` a real PostgreSQL node of that type carries.
 *      A relation the walk *can* produce — by reading it back from the parent,
 *      as a Bitmap Index Scan requires — is not a defect in the plan, so a
 *      captured plan is never rewritten. Verbatim stays verbatim.
 *   2. `summary` is replaced by the walk of the (possibly repaired) plan, so the
 *      fixture is internally consistent and
 *      `web/scripts/plan-conformance-audit.mjs` can prove it on every run.
 *
 * The *expectations* (`contract`, `expected_outcome`) are never touched: this
 * tool repairs the document's self-consistency, it does not decide verdicts.
 *
 *   node scripts/perf-fixture-normalize.mjs --check   # fail if any is stale
 *   node scripts/perf-fixture-normalize.mjs --write    # rewrite the stale ones
 */
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { scratchDir } from './lib/gateway.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..');
const fixturesDir = path.join(repoRoot, 'contracts', 'fixtures');

/* The bundle goes in the run's scratch root, so it leaves with the run. */
const out = path.join(scratchDir('bundle'), 'qc-plan-contract.cjs');
execFileSync(
  path.join(repoRoot, 'web', 'node_modules', '.bin', 'esbuild'),
  ['--bundle', '--format=cjs', '--platform=node', `--outfile=${out}`,
   path.join(repoRoot, 'web', 'src', 'api', 'planContract.ts')],
  { stdio: ['ignore', 'ignore', 'inherit'] },
);
const { summarizePlan } = createRequire(import.meta.url)(out);

const write = process.argv.includes('--write');
const isRecord = (v) => typeof v === 'object' && v !== null && !Array.isArray(v);

/** Depth-first pre-order, the order `summarizePlan` emits nodes in. */
function flatten(node, acc = []) {
  if (!isRecord(node) || typeof node['Node Type'] !== 'string') return acc;
  acc.push(node);
  if (Array.isArray(node.Plans)) for (const child of node.Plans) flatten(child, acc);
  return acc;
}

/** The relation a real node of this type carries, when the summary claimed one. */
function repairPlanNode(node, claimedRelation) {
  let changed = false;
  if (claimedRelation && node['Relation Name'] === undefined) {
    node['Relation Name'] = claimedRelation;
    if (node.Alias === undefined) node.Alias = claimedRelation;
    changed = true;
  }
  return changed;
}

const files = fs
  .readdirSync(fixturesDir)
  .filter((name) => name.startsWith('perf-') && name.endsWith('.json'))
  .sort();

let stale = 0;
for (const file of files) {
  const at = path.join(fixturesDir, file);
  const fx = JSON.parse(fs.readFileSync(at, 'utf8'));
  const bundle = fx.bundle;
  if (!isRecord(bundle) || !isRecord(bundle.summary)) {
    console.log(`  skip   ${file} (no bundle.summary)`);
    continue;
  }

  const claimed = (bundle.summary.nodes ?? []).map((n) => (n && n.relation) || null);
  const nodes = flatten(bundle.plan);
  let planChanged = false;
  const unreachable = [];
  nodes.forEach((node, i) => {
    if (!claimed[i]) return;
    /* Only a claim the walk cannot reach is a claim about the plan. A claim the
       walk already satisfies by inheritance means the plan is fine as captured
       and the summary (not the plan) was the stale half. */
    const derived = summarizePlan(bundle.plan).nodes[i];
    if (derived && derived.relation === claimed[i]) return;
    if (repairPlanNode(node, claimed[i])) planChanged = true;
    else unreachable.push(`node ${i} (${node['Node Type']}) claims ${claimed[i]}`);
  });
  for (const note of unreachable) console.log(`  warn   ${file} — ${note} could not be repaired`);

  const summary = summarizePlan(bundle.plan);
  const sameSummary = JSON.stringify(summary) === JSON.stringify(bundle.summary);
  if (sameSummary && !planChanged) continue;

  stale++;
  const reason = planChanged ? 'plan node gained the relation it never named' : 'summary was not the walk of its own plan';
  console.log(`  ${write ? 'fix   ' : 'stale '} ${file} — ${reason}`);
  if (write) {
    bundle.summary = summary;
    fs.writeFileSync(at, JSON.stringify(fx, null, 2) + '\n');
  }
}

if (stale === 0) {
  console.log(`perf-fixture-normalize: OK (${files.length} fixture${files.length === 1 ? '' : 's'} consistent)`);
  process.exit(0);
}
if (!write) {
  console.log(`\nperf-fixture-normalize: ${stale} fixture(s) stale — re-run with --write`);
  process.exit(1);
}
console.log(`\nperf-fixture-normalize: rewrote ${stale} fixture(s); re-run web/scripts/plan-conformance-audit.mjs`);
