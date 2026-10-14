import type { PGliteInterface } from '@electric-sql/pglite';
import type { ContentSection } from '../types';
import { CONTENT_FINGERPRINT_KEY, planContentSeed, splitSections } from '../lib/contentSeed';

/* Chapter reading content is authored as markdown (web/src/data/content/*.md),
   seeded into the app database as `app.content_docs` rows (the `app` schema,
   separate from the learner-visible `dataset` schema), and read back from there
   — so the database, not the bundle, is what the lesson page renders. */

const FILES = import.meta.glob('../data/content/*.md', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

/** The module id is the markdown file's name: `f3.md` is module `f3`. */
function moduleIdFromPath(path: string): string {
  const match = /([^/]+)\.md$/.exec(path);
  return match ? match[1] : path;
}

export const BUNDLED_CONTENT: Record<string, ContentSection[]> = (() => {
  const out: Record<string, ContentSection[]> = {};
  for (const [path, raw] of Object.entries(FILES)) {
    out[moduleIdFromPath(path)] = splitSections(raw);
  }
  return out;
})();

/**
 * Bring `app.content_docs` in line with the bundled markdown.
 *
 * Called on every boot: it is a no-op when the fingerprint matches and the
 * database holds every module, and rewrites the whole table otherwise. Content
 * is authored data, not learner data, so replacing it wholesale is correct —
 * the bundle is the source of truth and the table is a copy of it.
 */
export async function seedContentDocs(db: PGliteInterface): Promise<void> {
  let fingerprint: string | null = null;
  try {
    const meta = await db.query<{ value: string }>('SELECT value FROM app.content_meta WHERE key = $1', [
      CONTENT_FINGERPRINT_KEY,
    ]);
    fingerprint = meta.rows[0]?.value ?? null;
  } catch {
    /* `content_meta` ships with the schema. A database created before it existed
       reads as "no fingerprint", which re-seeds and then records one. */
  }
  const present = await db.query<{ module_id: string }>('SELECT DISTINCT module_id FROM app.content_docs');
  const plan = planContentSeed(
    { fingerprint, modules: present.rows.map((r) => r.module_id) },
    BUNDLED_CONTENT,
  );
  if (!plan) return;
  await db.transaction(async (tx) => {
    await tx.query('DELETE FROM app.content_docs');
    for (const row of plan.rows) {
      await tx.query(
        'INSERT INTO app.content_docs (course_id, module_id, ordinal, section, markdown) VALUES ($1, $2, $3, $4, $5)',
        [row.course_id, row.module_id, row.ordinal, row.section, row.markdown],
      );
    }
    await tx.query(
      'INSERT INTO app.content_meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = excluded.value',
      [CONTENT_FINGERPRINT_KEY, plan.fingerprint],
    );
  });
}
