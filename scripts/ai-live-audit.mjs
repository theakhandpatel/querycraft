/*
 * M4/M8 live-provider audit — the pluggable claim, tested against a real model.
 *
 * `make audit-ai` proves the pipeline with the deterministic fake provider.
 * This drives the same pipeline with a real OpenAI-compatible endpoint, which
 * is the only way to know the transport, the schema-constrained prompt and the
 * repair loop work against something that can actually be wrong.
 *
 * Two paths, one provider:
 *   - the challenge path (M4): one exercise, graded by the evaluator;
 *   - the course path (M8): one conversation. The model is asked for a small
 *     outline and then hands in one chapter per `save_chapter` tool call; the
 *     gateway executes and cross-checks each chapter in the course's own
 *     throwaway environment before appending it to the stored course, hands
 *     the engine's verdict back as the tool result, and drops — with its
 *     failure reported — a chapter the model cannot repair. The audit then
 *     re-runs the first exercise's reference solution through `/query` itself
 *     and re-derives the rows, because "the expected rows are the engine's
 *     answer" is the claim worth independent evidence. (The streaming half of
 *     the contract — a poll watching chapters_verified rise, and the record
 *     route answering with a `building` course, while later chapters are still
 *     being written — needs a provider that can be paused, so it is driven by
 *     scripts/course-gen-audit.mjs against a stub.)
 *
 * Configuration, in order of preference (never passed on the command line, so a
 * key stays out of shell history and process listings):
 *   AI_BASE_URL / AI_MODEL / AI_KEY / AI_KEY_FILE
 * or, when those are unset, the local mount a developer already has:
 *   ~/.config/opencode/opencode.json  (base URL and model)
 *   ~/.config/codex/amr-link.key      (the key)
 * When neither source resolves all three, the audit skips and says so — it
 * never silently passes.
 *
 * The audit asserts the pipeline's own honesty rather than a happy path: a real
 * model may produce a bundle the policy rejects, and that outcome must be
 * reported with the stage that refused and no active artifact. What must never
 * happen is an artifact being activated without passing the checks. Timings are
 * printed as evidence and are never asserted on.
 *
 * A refusal is a legitimate answer, not a failure of the pipeline — but it is
 * not evidence that the path under test works either. Where the run reaches a
 * refusal (the course path), the refusal is printed in full and the process
 * exits non-zero, so `make audit-ai-live` never reports OK for a path that was
 * never verified. The earlier skip is reserved for a missing provider/key,
 * which is the one case where the audit has nothing to say.
 */
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Reporter, buildGateway, getJSON, liveEnv, postJSON, startGateway, stopGateway } from './lib/gateway.mjs';

const report = new Reporter('ai-live-audit');

const HOME = os.homedir();
const OPENCODE_CONFIG = path.join(HOME, '.config', 'opencode', 'opencode.json');
const KEY_FILE_CANDIDATES = [
  process.env.AI_KEY_FILE,
  path.join(HOME, '.config', 'codex', 'amr-link.key'),
].filter(Boolean);

/**
 * opencodeMount reads the OpenAI-compatible endpoint a local opencode install
 * points at, so `make audit-ai-live` works for the developer who already
 * configured one. Nothing here is invented: the base URL and the model are the
 * ones the config itself names.
 */
function opencodeMount() {
  try {
    const cfg = JSON.parse(fs.readFileSync(OPENCODE_CONFIG, 'utf8'));
    const ref = typeof cfg.model === 'string' ? cfg.model : '';
    const slash = ref.indexOf('/');
    if (slash < 1) return null;
    const provider = cfg.providers?.[ref.slice(0, slash)];
    const base = provider?.settings?.baseURL;
    const model = ref.slice(slash + 1);
    if (!base || !model) return null;
    return { base, model, keyEnv: provider?.env?.[0] };
  } catch {
    return null; /* no config, or one this script cannot read: not a failure */
  }
}

const mount = opencodeMount();
const base = process.env.AI_BASE_URL || mount?.base || '';
const model = process.env.AI_MODEL || mount?.model || '';

/** resolveKey finds the key in the environment, then in the key files. */
function resolveKey() {
  if (process.env.AI_KEY) return { key: process.env.AI_KEY.trim(), source: 'AI_KEY' };
  if (mount?.keyEnv && process.env[mount.keyEnv]) return { key: process.env[mount.keyEnv].trim(), source: mount.keyEnv };
  for (const file of KEY_FILE_CANDIDATES) {
    try {
      const key = fs.readFileSync(file, 'utf8').trim();
      if (key) return { key, source: file };
    } catch { /* not there; try the next */ }
  }
  return { key: '', source: '' };
}

const { key, source: keySource } = resolveKey();
const sourceOf = process.env.AI_BASE_URL && process.env.AI_MODEL ? 'the environment' : OPENCODE_CONFIG;

/** redact removes the key from anything this script prints. */
const redact = (s) => (key ? String(s).split(key).join('<redacted>') : String(s));

/* ---------- independent comparison (the audit's own grading, not the product's) ---------- */

/** sameCell compares one cell; numbers within tolerance, everything else exactly. */
function sameCell(a, b, tol) {
  if (a === b) return true;
  if (typeof a === 'number' && typeof b === 'number') return Math.abs(a - b) <= Math.max(tol, 1e-9);
  return false;
}

function sameRow(a, b, tol) {
  return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((v, i) => sameCell(v, b[i], tol));
}

/**
 * rowsMatch says whether actual reproduces expected cell by cell. ordered
 * follows the plan's own row_order rule: an "exact" contract compares row i to
 * row i, an "ignored" one compares the multiset of rows.
 */
function rowsMatch(expected, actual, tol, ordered) {
  if (!Array.isArray(expected) || !Array.isArray(actual) || expected.length !== actual.length) return false;
  if (!ordered) {
    const used = new Array(actual.length).fill(false);
    return expected.every((want) => {
      const i = actual.findIndex((got, j) => !used[j] && sameRow(want, got, tol));
      if (i < 0) return false;
      used[i] = true;
      return true;
    });
  }
  return expected.every((want, i) => sameRow(want, actual[i], tol));
}

const sameStrings = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);
const previewRows = (rows, n = 3) => JSON.stringify((rows ?? []).slice(0, n));
const quoteIdent = (name) => `"${String(name).replace(/"/g, '""')}"`;

/* ---------- printing ---------- */

/** dumpOutcome prints an outcome envelope in full: stages with their timings, repairs, error, evidence. */
function dumpOutcome(label, res, seconds) {
  console.log(`  ..   ${label}: HTTP ${res.status} in ${seconds}s, status=${res.json?.status} kind=${res.json?.kind ?? '-'}`);
  for (const s of res.json?.stages ?? []) {
    console.log(`  ..     ${s.name}: ${s.state}${s.ms ? ` in ${s.ms} ms` : ''} — ${redact(s.detail ?? '')}`);
  }
  for (const a of res.json?.attempts ?? []) {
    console.log(`  ..     repair ${a.number} after ${a.stage} failed: ${redact(a.detail)}`);
  }
  if (res.json?.error) {
    const e = res.json.error;
    console.log(`  ..   error: ${e.code} @ ${e.stage ?? '-'} — ${redact(e.message)}${e.detail ? ' — ' + redact(e.detail) : ''}`);
  }
  if (res.json?.evidence) console.log(`  ..   evidence: ${redact(JSON.stringify(res.json.evidence))}`);
}

/** dumpChapterReport prints what verification measured for each planned exercise. */
function dumpChapterReport(chapters) {
  let total = 0;
  for (const ch of chapters ?? []) {
    const exs = (ch.exercises ?? []).map((e) => {
      total++;
      const err = e.error ? ` error=${e.error.code}@${e.error.stage ?? '-'} ${redact(e.error.message)}` : '';
      return `${e.id}:${e.status} ref=${e.reference_rows ?? 0} cross=${e.cross_check_rows ?? 0} fixtures=${e.fixtures_rejected ?? 0}/${e.fixtures_total ?? 0} ${e.ms ?? 0}ms${err}`;
    });
    console.log(`  ..     chapter ${ch.id} — ${ch.title}: ${exs.length ? exs.join(' | ') : 'no exercise reached verification'}`);
  }
  console.log(`  ..   the chapter report covers ${total} exercise(s)`);
}

/* ---------- the course path ---------- */

const courseGoal = 'I know basic SQL but I struggle with analytical interview questions. Teach me to combine CTEs, window functions and conditional aggregation on realistic commerce data.';

const courseRequest = {
  title: 'Analytical SQL for interviews: CTEs, window functions and conditional aggregation',
  scenario: courseGoal,
  topics: ['CTEs', 'window functions', 'conditional aggregation'],
  difficulty: 'intermediate',
  exercise_count: 4,
};

/** assertActiveCourse holds the checks that only an activated plan can carry. */
async function assertActiveCourse(gw, auth, course, model) {
  const plan = course.json.plan;
  const chapters = course.json.chapters ?? [];
  const planned = [];
  for (const ch of plan.chapters ?? []) {
    for (const ex of ch.exercises ?? []) planned.push(ex);
  }

  report.section('the activated course plan is the shape a course must be');
  report.check('the plan has at least one chapter', (plan.chapters ?? []).length >= 1, (plan.chapters ?? []).length);
  /* A chapter's teaching is its markdown: at least one section must actually
     carry a body. (The pipeline itself refuses a section shorter than its own
     40-character floor, so "non-empty" is the audit stating the same rule from
     the outside.) */
  const nonEmpty = (ch) => (ch.sections ?? []).filter((s) => typeof s.body === 'string' && s.body.trim().length > 0);
  const emptyProse = (plan.chapters ?? []).filter((ch) => nonEmpty(ch).length === 0);
  report.check(
    'every chapter carries at least one non-empty markdown section',
    emptyProse.length === 0,
    (plan.chapters ?? []).map((ch) => `${ch.id}: ${nonEmpty(ch).length}/${(ch.sections ?? []).length} non-empty section(s)`),
  );
  const noRows = planned.filter((ex) => !(Array.isArray(ex.expected?.rows) && ex.expected.rows.length > 0)).map((ex) => ex.id);
  report.check('every exercise carries engine-measured expected rows', noRows.length === 0, noRows);
  const badColumns = planned
    .filter((ex) => !sameStrings(ex.expected?.columns ?? [], ex.result_contract?.columns ?? []))
    .map((ex) => `${ex.id}: expected=${JSON.stringify(ex.expected?.columns)} contract=${JSON.stringify(ex.result_contract?.columns)}`);
  report.check('every expected.columns list matches its result contract', badColumns.length === 0, badColumns);
  console.log(`  ..   plan ${plan.id} — ${(plan.chapters ?? []).length} chapter(s), ${planned.length} exercise(s), ${planned.reduce((n, ex) => n + (ex.expected?.rows?.length ?? 0), 0)} expected row(s) in total`);

  report.section('the per-exercise report is the verification, not a claim');
  const byID = new Map();
  for (const ch of chapters) {
    for (const ex of ch.exercises ?? []) byID.set(ex.id, ex);
  }
  /* Under M8 the report carries one entry per *planned* chapter, so it can be
     longer than the plan: a chapter the model could not repair is dropped, and
     what the plan holds is exactly the chapters that verified. The claims that
     must hold are therefore "every exercise in the plan is reported verified"
     and "every chapter the report calls dropped names the stage that refused
     it" — not "the report is the plan". */
  const droppedChapters = chapters.filter((ch) => ch.status && ch.status !== 'verified');
  const missing = planned
    .filter((ex) => byID.get(ex.id)?.status !== 'verified')
    .map((ex) => `${ex.id}:${byID.get(ex.id)?.status ?? 'unreported'}`);
  report.check('every exercise in the plan is reported verified', missing.length === 0, missing);
  report.check('every chapter the report does not call verified names the stage that refused it',
    droppedChapters.every((ch) => !!ch.error?.stage),
    droppedChapters.map((ch) => `${ch.id}:${ch.status}:${ch.error?.stage ?? 'no stage'}`));
  console.log(`  ..   report: ${chapters.length} planned chapter(s), ${droppedChapters.length} dropped, ${chapters.reduce((n, ch) => n + (ch.exercises ?? []).length, 0)} exercise entr(ies)`);
  const notVerified = [];
  const unreconciled = [];
  const unproven = [];
  for (const ex of planned) {
    const r = byID.get(ex.id);
    if (!r || r.status !== 'verified') {
      notVerified.push(`${ex.id}:${r ? r.status : 'missing'}`);
      continue;
    }
    if (!(Number(r.reference_rows) > 0) || Number(r.cross_check_rows) !== Number(r.reference_rows)) {
      unreconciled.push(`${ex.id}(ref=${r.reference_rows},cross=${r.cross_check_rows})`);
    }
    if (!(Number(r.fixtures_total) > 0) || Number(r.fixtures_rejected) !== Number(r.fixtures_total)) {
      unproven.push(`${ex.id}(${r.fixtures_rejected}/${r.fixtures_total})`);
    }
  }
  report.check('no exercise in the plan is left unreported', notVerified.length === 0, notVerified);
  report.check('every cross-check returned the reference rows', unreconciled.length === 0, unreconciled);
  report.check('every exercise rejected every adversarial fixture', unproven.length === 0, unproven);
  report.check('the plan carries real provenance', plan.provenance?.provider === 'openai-compatible' && !!plan.provenance?.model, redact(JSON.stringify(plan.provenance)));
  report.check('the model is attributed, not the transport default', plan.provenance?.model === model, String(plan.provenance?.model));

  report.section('the expected rows are the engine’s answer, re-checked by this audit');
  // The first exercise of the plan, in teaching order: chapter 0 may carry
  // prose only, so the first exercise is the first chapter that has one.
  const first = planned[0];
  const setup = plan.setup?.statements ?? [];
  const firstChapter = (plan.chapters ?? []).find((ch) => (ch.exercises ?? []).some((ex) => ex.id === first.id));
  console.log(`  ..   first exercise ${first.id} — ${first.title} (chapter ${firstChapter?.id ?? '-'})`);
  console.log(`  ..   reference_solution: ${first.reference_solution}`);
  console.log(`  ..   expected columns: ${JSON.stringify(first.expected?.columns)}, ${first.expected?.rows?.length ?? 0} row(s)`);
  for (const stmt of setup) {
    const applied = await postJSON(`${gw.base}/query`, { sql: stmt }, 60_000, auth);
    report.check(`the plan's own setup statement applies: ${redact(stmt)}`, applied.json?.status === 'succeeded', redact(JSON.stringify(applied.json?.error ?? applied.json?.status)));
  }

  const tol = Number(first.result_contract?.numeric_tolerance ?? 0);
  const ordered = first.result_contract?.row_order === 'exact';
  console.log(`  ..   row rule: ${ordered ? 'exact' : 'ignored'} order, tolerance ${tol}`);

  const regradeStarted = Date.now();
  const refRun = await postJSON(`${gw.base}/query`, { sql: first.reference_solution }, 120_000, auth);
  const gotCols = (refRun.json?.columns ?? []).map((c) => c.name);
  report.check('the engine re-runs the reference solution', refRun.json?.status === 'succeeded', redact(JSON.stringify(refRun.json?.error ?? refRun.json?.status)));
  report.check('the engine returns the columns the plan names, in order', sameStrings(gotCols, first.expected?.columns ?? []), { want: first.expected?.columns, got: gotCols });
  const regrade = rowsMatch(first.expected?.rows ?? [], refRun.json?.rows ?? [], tol, ordered);
  report.check('the re-run reproduces the expected rows cell by cell', regrade, { want: previewRows(first.expected?.rows), got: previewRows(refRun.json?.rows) });
  console.log(`  ..   re-grade want: ${previewRows(first.expected?.rows)}`);
  console.log(`  ..   re-grade got:  ${previewRows(refRun.json?.rows)}`);

  const wrongColumn = (first.expected?.columns ?? ['n'])[0];
  const wrongSQL = `SELECT 1 AS ${quoteIdent(wrongColumn)}`;
  const wrongRun = await postJSON(`${gw.base}/query`, { sql: wrongSQL }, 60_000, auth);
  const wrongMatches = wrongRun.json?.status === 'succeeded' && rowsMatch(first.expected?.rows ?? [], wrongRun.json?.rows ?? [], tol, ordered);
  report.check(`a deliberately wrong query (${wrongSQL}) does not reproduce the expected rows`, !wrongMatches, { sql: wrongSQL, got: previewRows(wrongRun.json?.rows) });
  console.log(`  ..   wrong query returned: ${previewRows(wrongRun.json?.rows)}`);
  console.log(`  ..   the independent re-grade took ${((Date.now() - regradeStarted) / 1000).toFixed(1)}s of wall clock`);
}

async function main() {
  if (!base || !model || !key) {
    report.skip('AI_BASE_URL / AI_MODEL / AI_KEY(_FILE) not all set, and no local mount supplied them (looked at ' + OPENCODE_CONFIG + ' and ' + KEY_FILE_CANDIDATES.join(', ') + ')');
    report.finish();
    return;
  }

  const bin = buildGateway('ai-live');
  const env = await liveEnv('ai-live');
  const gw = await startGateway(bin, {
    ...env,
    ai_provider: 'openai-compatible',
    ai_base_url: base,
    ai_model: model,
    ai_key: key,
    ai_timeout_ms: 120000,
    ai_run_timeout_ms: 600000,
  });

  console.log(`  ..   provider ${base} model ${model} (base/model from ${sourceOf}, key from ${keySource})`);

  try {
    const caps = await getJSON(`${gw.base}/capabilities`);
    report.section('the real provider is wired in');
    report.check('the lab is available', caps.json?.ai?.available === true, JSON.stringify(caps.json?.ai));
    report.check('the provider names the transport', caps.json?.ai?.provider === 'openai-compatible', String(caps.json?.ai?.provider));
    report.check('the model is the configured one', caps.json?.ai?.model === model, String(caps.json?.ai?.model));
    report.check('it is not advertised as deterministic', caps.json?.ai?.deterministic === false, String(caps.json?.ai?.deterministic));

    const signup = await postJSON(`${gw.base}/auth/signup`, {
      email: `ai-live-${process.pid}@querycraft.local`,
      name: 'Live Audit',
      password: 'ai-live-passphrase',
    });
    const token = signup.json?.token;
    if (!token) throw new Error(`signup failed: HTTP ${signup.status} ${redact(signup.body).slice(0, 400)}`);
    const auth = { authorization: `Bearer ${token}` };

    report.section('a real model generates through the same pipeline');
    const started = Date.now();
    const gen = await postJSON(
      `${gw.base}/challenges/generate`,
      {
        topics: ['joins', 'aggregation'],
        difficulty: 'beginner',
        scenario: 'Which customers have never placed an order?',
        constraints: ['one statement', 'return the customer name only'],
      },
      180_000,
      auth,
    );
    const took = ((Date.now() - started) / 1000).toFixed(1);
    dumpOutcome('challenge', gen, took);

    report.check('the request was processed, not crashed', gen.status < 500 || gen.status === 503, `HTTP ${gen.status}`);
    report.check('an outcome envelope came back', typeof gen.json?.status === 'string', redact(gen.body).slice(0, 300));
    report.check(
      'an activated artifact passed every stage',
      gen.json?.status !== 'active' || (gen.json?.stages ?? []).every((s) => s.state === 'done' || s.name === 'repair'),
      redact(JSON.stringify(gen.json?.stages)),
    );
    report.check(
      'a rejected run names the stage that refused',
      gen.json?.status !== 'rejected' || typeof gen.json?.error?.stage === 'string',
      redact(JSON.stringify(gen.json?.error)),
    );
    report.check('only an active run returns an artifact', gen.json?.status === 'active' ? !!gen.json?.artifact : gen.json?.artifact == null);

    if (gen.json?.status === 'active') {
      const artifact = gen.json.artifact;
      report.section('the activated artifact is graded by the evaluator');
      const exercise = {
        id: artifact.id,
        version: artifact.version ?? 1,
        lesson_id: artifact.lesson_id ?? 'live',
        type: artifact.type ?? 'solve',
        prompt: artifact.prompt ?? '',
        setup: artifact.setup,
        result_contract: artifact.result_contract,
        reference_solution: artifact.reference_solution,
        hints: artifact.hints ?? [],
      };
      const ref = await postJSON(`${gw.base}/answers/evaluate`, { sql: artifact.reference_solution, exercise }, 60_000, auth);
      report.check('the generated reference passes its own contract', ref.json?.passed === true, `passed=${ref.json?.passed}`);
      const wrong = await postJSON(`${gw.base}/answers/evaluate`, { sql: 'SELECT 1 AS n', exercise }, 60_000, auth);
      report.check('a wrong query does not pass', wrong.json?.passed === false, `passed=${wrong.json?.passed}`);
      report.check('the artifact carries real provenance', artifact.provenance?.provider === 'openai-compatible' && !!artifact.provenance?.model, redact(JSON.stringify(artifact.provenance)));
      report.check('the model is attributed, not the transport default', artifact.provenance?.model === model, String(artifact.provenance?.model));
    } else {
      report.skip('challenge activation', 'the model did not produce an acceptable bundle this run; the pipeline refused it honestly and the refusal is printed above');
    }

    /* ---------- the course path (M8) ---------- */
    report.section('a real model writes a whole course, one chapter at a time, through the same pipeline');
    const courseStarted = Date.now();
    const course = await postJSON(`${gw.base}/courses/generate`, courseRequest, 660_000, auth);
    const courseTook = ((Date.now() - courseStarted) / 1000).toFixed(1);
    dumpOutcome('course', course, courseTook);
    dumpChapterReport(course.json?.chapters);
    console.log(`  ..   learner goal: ${courseGoal}`);

    report.check('the course request was processed, not crashed', course.status < 500 || course.status === 503, `HTTP ${course.status}`);
    report.check(
      'a course outcome envelope came back',
      typeof course.json?.status === 'string' && course.json?.kind === 'course',
      redact(course.body).slice(0, 300),
    );
    report.check(
      'only an active course returns a plan',
      course.json?.status === 'active' ? !!course.json?.plan : course.json?.plan == null,
      `status=${course.json?.status} plan=${course.json?.plan ? 'present' : 'absent'}`,
    );
    // A transport failure (AI_UNAVAILABLE) has no pipeline stage to name: it
    // never reached one. A rejection by the pipeline always does, because the
    // stage is the useful part of the refusal.
    report.check(
      'a refused course names its error code',
      course.json?.status === 'active' || typeof course.json?.error?.code === 'string',
      redact(JSON.stringify(course.json?.error)),
    );
    report.check(
      'a rejected course names the stage that refused',
      course.json?.status !== 'rejected' || typeof course.json?.error?.stage === 'string',
      redact(JSON.stringify(course.json?.error)),
    );

    if (course.json?.status === 'active') {
      await assertActiveCourse(gw, auth, course, model);
    } else {
      /* A refusal is a legitimate answer from a real model: a bundle this
         pipeline will not activate is the honesty under test, and the envelope
         above already printed the code, the stage, the per-stage timings and
         the per-exercise report. It is not evidence that the course path
         works, though, so the run exits non-zero. A skip here would print
         `ai-live-audit: OK` for a course that was never verified. */
      const err = course.json?.error ?? {};
      const stageLine = (course.json?.stages ?? []).map((s) => `${s.name}=${s.state}`).join(' ');
      report.section('the course was refused');
      console.log(`  ..   status=${course.json?.status ?? '(none)'} code=${err.code ?? '-'} stage=${err.stage ?? '-'}`);
      console.log(`  ..   message: ${redact(err.message ?? '(none)')}`);
      if (err.detail) console.log(`  ..   detail: ${redact(err.detail)}`);
      console.log(`  ..   stages: ${stageLine || '(none)'}`);
      console.log('  ..   the per-exercise report, as far as the run reached it, is printed above in full');
      report.check(
        'the real model activated a course that passed every verification stage',
        false,
        `status=${course.json?.status}${err.code ? ` code=${err.code}` : ''}${err.stage ? ` stage=${err.stage}` : ''}`,
      );
    }
  } finally {
    stopGateway(gw.child);
  }

  report.finish();
}

main().catch((err) => {
  console.error(`ai-live-audit: FAILED — ${redact(err?.message ?? err)}`);
  process.exitCode = 1;
});
