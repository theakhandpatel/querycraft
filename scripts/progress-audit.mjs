/*
 * Progress-model audit.
 *
 * The Progress screen used to print `attempts * 3 + 6` minutes of study — a
 * number with no source, which made a live workspace look like a mock. These
 * checks pin the honest behaviour that replaced it: measured time only, a
 * duration formatter that never invents a value, and a v4 -> v5 upgrade that
 * keeps a learner's real passes instead of discarding them.
 *
 *   make audit-progress          # bundles src/lib/progress.ts and runs this
 *   node scripts/progress-audit.mjs
 */
import { createRequire } from 'node:module';
const require = createRequire('/tmp/');
const p = require('/tmp/progress.cjs');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — got ' + JSON.stringify(detail)}`);
  }
}

console.log(`shape version: ${p.PROGRESS_VERSION}`);
check('PROGRESS_VERSION is 5', p.PROGRESS_VERSION === 5, p.PROGRESS_VERSION);

const empty = p.emptyProgress();
check('emptyProgress starts with no attempts', empty.attempts === 0 && empty.events.length === 0);
check('emptyProgress is not sample data', p.isSampleProgress(empty) === false);
check('emptyProgress carries an empty timeMs map', empty.timeMs && Object.keys(empty.timeMs).length === 0, empty.timeMs);

console.log('formatDuration');
check('no time at all reads as an em dash, never a made-up baseline', p.formatDuration(0) === '—', p.formatDuration(0));
check('a sub-minute sample is labelled, not rounded up', p.formatDuration(30000) === '<1 min', p.formatDuration(30000));
check('one minute', p.formatDuration(60000) === '1 min', p.formatDuration(60000));
check('twenty-five minutes', p.formatDuration(25 * 60000) === '25 min', p.formatDuration(25 * 60000));
check('exactly one hour drops the minute remainder', p.formatDuration(3600000) === '1 h', p.formatDuration(3600000));
check('ninety minutes keeps the remainder', p.formatDuration(90 * 60000) === '1 h 30 min', p.formatDuration(90 * 60000));

console.log('accrueStudyTime');
check('a hidden tab is not study time', p.accrueStudyTime(5000, 30000, true) === 5000, p.accrueStudyTime(5000, 30000, true));
check('a visible second adds a second', p.accrueStudyTime(5000, 1000, false) === 6000, p.accrueStudyTime(5000, 1000, false));
check('a suspend window is capped at one minute', p.accrueStudyTime(0, 9 * 3600000, false) === 60000, p.accrueStudyTime(0, 9 * 3600000, false));
check('a clock that goes backwards cannot subtract', p.accrueStudyTime(4000, -8000, false) === 4000, p.accrueStudyTime(4000, -8000, false));
check('hidden cannot drive the bucket negative', p.accrueStudyTime(-50, 10, true) === 0, p.accrueStudyTime(-50, 10, true));

console.log('migrateProgress');
const v5 = { version: 5, seeded: false, events: [], passed: {}, accepted: {}, firstTry: {}, done: {}, notes: [], saved: [], attempts: 0, attemptsByDay: [], timeMs: { joins: 1000 } };
check('a current snapshot passes through untouched', p.migrateProgress(v5) === v5);
const v4 = {
  version: 4, seeded: false, attempts: 2, attemptsByDay: [], notes: [], saved: [],
  events: [{ ts: 1, id: 'ex.1.1', kind: 'passed', title: 't', course: 'sql-analytics', lesson: 0 }],
  passed: { 'ex.1.1': true }, accepted: {}, firstTry: { 'ex.1.1': true }, done: { m1: true },
};
const up = p.migrateProgress(v4);
check('v4 is upgraded, not dropped', !!up && up.version === 5, up && up.version);
check('upgrade keeps the real passes', !!up && up.passed['ex.1.1'] === true && up.firstTry['ex.1.1'] === true);
check('upgrade keeps chapter completion', !!up && up.done.m1 === true);
check('upgrade adds an empty measured-time map', !!up && !!up.timeMs && Object.keys(up.timeMs).length === 0, up && up.timeMs);
check('v1 ids are too old to migrate, so they are discarded', p.migrateProgress({ version: 1, passed: { 'where.1': true } }) === null);
check('garbage is rejected', p.migrateProgress('nope') === null && p.migrateProgress(null) === null);

console.log('rebuildProgressFromEvents');
const rebuilt = p.rebuildProgressFromEvents({ ...v5, timeMs: { joins: 4242 }, events: v4.events });
check('a rebuild does not wipe measured time', rebuilt.timeMs.joins === 4242, rebuilt.timeMs);
check('a rebuild recomputes attempts from events', rebuilt.attempts === 1, rebuilt.attempts);

console.log('attempt version stamps (design 7.3)');
/* Every attempt records the content versions it was made against, so a later
   change to the dataset or the exercise shows "solved on an earlier version"
   instead of silently invalidating a pass. */
const stamp = p.stampFor({ id: 'ex.1.1' }, 1);
check('an exercise with no declared version is version 1', stamp.exerciseVersion === 1, stamp);
check('the stamp carries the dataset version', stamp.datasetVersion === 1, stamp);
const bumped = p.stampFor({ id: 'ex.1.1', version: 3 }, 2);
check('a declared version is used as-is', bumped.exerciseVersion === 3, bumped);
const attempt = { ts: 1, id: 'ex.1.1', kind: 'passed', title: 't', course: 'sql-analytics', lesson: 0, datasetVersion: 1, exerciseVersion: 1 };
check('a matching attempt is current, not stale', p.isEarlierVersion(attempt, { datasetVersion: 1, exerciseVersion: 1 }) === false);
check('a bumped exercise makes the old pass earlier-version', p.isEarlierVersion(attempt, { datasetVersion: 1, exerciseVersion: 2 }) === true);
check('a bumped dataset makes the old pass earlier-version', p.isEarlierVersion(attempt, { datasetVersion: 2, exerciseVersion: 1 }) === true);
check('an unstamped attempt is never called stale', p.isEarlierVersion({ ts: 1, id: 'ex.1.1', kind: 'passed', title: 't', course: 'c', lesson: 0 }, { datasetVersion: 9, exerciseVersion: 9 }) === false);

console.log('sample data');
const sample = p.seedProgress();
check('sample data is marked as sample', p.isSampleProgress(sample) === true);
check('sample data carries sample finish times', !!sample.timeMs && sample.timeMs['sql-analytics'] > 0, sample.timeMs);

if (failed) {
  console.log(`\nprogress-audit: ${failed} check(s) failed`);
  process.exit(1);
}
console.log('\nprogress-audit: OK');
