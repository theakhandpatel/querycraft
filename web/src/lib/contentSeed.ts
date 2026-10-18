/*
 * The content seeder's decision, separated from the bundling that feeds it.
 *
 * Chapter content is authored as markdown, bundled by Vite (`import.meta.glob`),
 * written into `app.content_docs`, and read back from there — the database is
 * what the lesson page renders, and the bundle is only the seeder.
 *
 * Seeding "once, if the table is empty" breaks the moment a module is authored:
 * the table is not empty, so the new module never gets rows and its lesson
 * renders blank. The seeder is therefore *ensure*, not *once* — a fingerprint of
 * the bundle is stored beside the rows and the content is rewritten whenever it
 * no longer matches.
 *
 * The decision is pure and lives here rather than in `api/content.ts`, which is
 * where the Vite-only `import.meta.glob` lives. `scripts/content-audit.mjs`
 * drives these functions against the real markdown on disk, so the rule is
 * checked without a browser.
 */
import type { ContentSection } from '../types';

interface ContentRow {
  course_id: string;
  module_id: string;
  ordinal: number;
  section: string;
  markdown: string;
}

/** Key in `app.content_meta` holding the fingerprint of the seeded bundle. */
export const CONTENT_FINGERPRINT_KEY = 'content_fingerprint';

interface StoredContent {
  /** The fingerprint recorded when the rows were last written, or null. */
  fingerprint: string | null;
  /** The module ids the database currently holds. */
  modules: string[];
}

/** A module id names its course by prefix; the ids are authored and stable.
    Every built course needs a row here — a prefix with no entry falls through
    to the default and the chapter's markdown is seeded onto the wrong course,
    which is what happened when the `plans` course shipped without one. */
const COURSE_BY_PREFIX: Record<string, string> = {
  m: 'sql-analytics',
  j: 'joins',
  q: 'subqueries',
  f: 'foundations',
  p: 'plans',
  s: 'sqlite-essentials',
  r: 'sqlite-in-production',
  x: 'postgres-to-sqlite',
};

export function courseForModule(moduleId: string): string {
  return COURSE_BY_PREFIX[moduleId.slice(0, 1)] || 'sql-analytics';
}

/**
 * Split a document into its `##` sections. Text before the first `##` (after the
 * `#` title) becomes an "Overview" section so nothing is dropped.
 */
export function splitSections(markdown: string): ContentSection[] {
  const sections: { section: string; lines: string[] }[] = [];
  let current: { section: string; lines: string[] } = { section: 'Overview', lines: [] };
  for (const line of markdown.split(/\r?\n/)) {
    const heading = /^##\s+(.*\S)\s*$/.exec(line);
    if (heading) {
      sections.push(current);
      current = { section: heading[1].replace(/[`*]/g, '').trim(), lines: [] };
      continue;
    }
    if (/^#\s+/.test(line)) continue;
    current.lines.push(line);
  }
  sections.push(current);
  return sections
    .map((s) => ({ section: s.section, markdown: s.lines.join('\n').trim() }))
    .filter((s) => s.markdown.length > 0);
}

/** FNV-1a, hex. Not a security hash — it only has to change when the text does. */
function fnv1a(input: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16).padStart(8, '0');
}

/** Modules are walked in sorted order, so a reordering of the glob keys is not
    a content change. */
export function contentFingerprint(bundled: Record<string, ContentSection[]>): string {
  const parts: string[] = [];
  for (const moduleId of Object.keys(bundled).sort()) {
    parts.push(moduleId);
    for (const section of bundled[moduleId]) parts.push(section.section, section.markdown);
  }
  return fnv1a(parts.join('\u0000'));
}

/** Every row the bundle should produce. */
function contentRows(bundled: Record<string, ContentSection[]>): ContentRow[] {
  const rows: ContentRow[] = [];
  for (const moduleId of Object.keys(bundled).sort()) {
    const courseId = courseForModule(moduleId);
    bundled[moduleId].forEach((section, ordinal) => {
      rows.push({
        course_id: courseId,
        module_id: moduleId,
        ordinal,
        section: section.section,
        markdown: section.markdown,
      });
    });
  }
  return rows;
}

/**
 * What the seeder should write, or null when the database already holds this
 * bundle. A database from a build that stored no fingerprint reads as
 * `fingerprint: null`, which never matches, so it is rewritten once and gains
 * one — and a database holding only *some* of the modules is rewritten even if
 * the fingerprint somehow matches.
 */
export function planContentSeed(
  stored: StoredContent,
  bundled: Record<string, ContentSection[]>,
): { fingerprint: string; rows: ContentRow[] } | null {
  const expected = Object.keys(bundled).sort();
  const fingerprint = contentFingerprint(bundled);
  const have = new Set(stored.modules);
  const complete = expected.every((id) => have.has(id));
  if (stored.fingerprint === fingerprint && complete) return null;
  return { fingerprint, rows: contentRows(bundled) };
}
