/*
 * A freshness guard for the audits that read a bundle `make` builds into /tmp.
 *
 * `make audit-lessons` compiles web/src/data/curriculum.ts to
 * /tmp/curriculum.cjs and then runs the script. Running the script on its own
 * skips the compile and audits whatever bundle happens to be in /tmp — which is
 * how a change to the course catalog passed a content audit it should have
 * failed, and how a new course went unnoticed by the lesson and solution audits.
 *
 * The source of the mistake is that a stale bundle is *valid*: it parses, it has
 * the right shape, and it is one revision old. There is no way for the audit to
 * tell from the data, so it compares timestamps instead and says which make
 * target to run.
 */
import { statSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

/**
 * assertFreshBundle fails unless every source file is older than the bundle.
 * @param {string} bundle absolute path to the compiled bundle
 * @param {string} target the make target that compiles it, for the message
 * @param {string[]} sources repo-relative source files the bundle is built from
 */
export function assertFreshBundle(bundle, target, sources) {
  let bundleMtime;
  try {
    bundleMtime = statSync(bundle).mtimeMs;
  } catch {
    console.error(`${bundle} is missing — run 'make ${target}' (it compiles the bundle first).`);
    process.exit(1);
  }
  const stale = sources
    .map((s) => resolve(ROOT, s))
    .filter((s) => {
      try {
        return statSync(s).mtimeMs > bundleMtime;
      } catch {
        return false;
      }
    })
    .map((s) => s.slice(ROOT.length + 1));
  if (stale.length > 0) {
    console.error(
      `${bundle} is stale: ${stale.join(', ')} changed after it was built.\n` +
        `Run 'make ${target}' instead of the script directly, or the audit will judge an older revision.`,
    );
    process.exit(1);
  }
}
