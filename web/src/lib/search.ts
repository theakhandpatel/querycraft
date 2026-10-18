/*
 * Client-side search over the workbook (design §14 M2, §10).
 *
 * Deliberately plain: lowercase substring matching with an AND across the
 * query's words, ranked by where the match landed (title outranks body) and by
 * what kind of thing matched. No fuzzy matching, no synonyms, no typo
 * correction — a search that silently guesses is a search a learner cannot
 * trust. An empty query returns nothing rather than everything.
 *
 * Pure and storage-free, so `scripts/search-audit.mjs` can decide ranking
 * without a browser.
 */
import type { Course, ContentSection, Progress } from '../types';

export type SearchKind = 'lesson' | 'objective' | 'section' | 'exercise' | 'note' | 'bookmark' | 'saved';

export interface SearchDoc {
  id: string;
  kind: SearchKind;
  course: string;
  courseTitle: string;
  moduleId: string;
  /** 1-based chapter number. */
  lesson: number;
  /** Outline index of the section, when the hit is one. */
  section?: number;
  /** Exercise id, when the hit is an exercise cell. */
  exercise?: string;
  title: string;
  text: string;
}

interface SearchHit {
  doc: SearchDoc;
  score: number;
}

/** A stable ordering between kinds at equal text score. */
const KIND_WEIGHT: Record<SearchKind, number> = {
  lesson: 6,
  exercise: 5,
  section: 4,
  objective: 3,
  bookmark: 3,
  note: 3,
  saved: 3,
};

/** Strip markdown down to words, so a `##` heading does not outrank prose. */
function plain(markdown: string): string {
  return String(markdown || '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[#>*_|~]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

export function buildIndex(input: {
  courses: Course[];
  content?: Record<string, ContentSection[]>;
  progress?: Progress;
}): SearchDoc[] {
  const docs: SearchDoc[] = [];
  const content = input.content || {};
  const courses = input.courses || [];

  const moduleIdFor = (course: Course, lesson: number): string => {
    const list = course.modules || [];
    const found = list.find((m, i) => (m.n || i + 1) === lesson);
    return found ? found.id : list[lesson - 1]?.id ?? '';
  };

  for (const course of courses) {
    (course.modules || []).forEach((m, i) => {
      const lesson = m.n || i + 1;
      docs.push({
        id: `lesson:${course.id}:${m.id}`,
        kind: 'lesson',
        course: course.id,
        courseTitle: course.title,
        moduleId: m.id,
        lesson,
        title: m.title,
        text: [m.summary, ...(m.objectives || [])].join(' '),
      });
      (m.objectives || []).forEach((o, oi) => {
        docs.push({
          id: `objective:${course.id}:${m.id}:${oi}`,
          kind: 'objective',
          course: course.id,
          courseTitle: course.title,
          moduleId: m.id,
          lesson,
          title: o,
          text: m.title,
        });
      });
      (m.blocks || []).forEach((b) => {
        if (b.type !== 'exercise' || !b.id) return;
        docs.push({
          id: `exercise:${course.id}:${b.id}`,
          kind: 'exercise',
          course: course.id,
          courseTitle: course.title,
          moduleId: m.id,
          lesson,
          exercise: b.id,
          title: `Exercise ${b.id.replace('ex.', '')}`,
          text: plain(String(b.prompt || '')),
        });
      });
      (content[m.id] || []).forEach((sec, si) => {
        docs.push({
          id: `section:${course.id}:${m.id}:${si}`,
          kind: 'section',
          course: course.id,
          courseTitle: course.title,
          moduleId: m.id,
          lesson,
          section: si,
          title: sec.section,
          text: plain(sec.markdown),
        });
      });
    });
  }

  const progress = input.progress;
  if (progress) {
    for (const n of progress.notes || []) {
      const course = courses.find((c) => c.id === n.course);
      if (!course) continue;
      const isBookmark = n.kind === 'bookmark';
      docs.push({
        id: `${isBookmark ? 'bookmark' : 'note'}:${n.course}:${n.ts}`,
        kind: isBookmark ? 'bookmark' : 'note',
        course: n.course,
        courseTitle: course.title,
        moduleId: moduleIdFor(course, n.lesson),
        lesson: n.lesson,
        section: n.section,
        title: isBookmark ? `Bookmark · chapter ${n.lesson}` : `Note · chapter ${n.lesson}`,
        text: String(n.text || ''),
      });
    }
    for (const q of progress.saved || []) {
      const course = courses.find((c) => c.id === q.course);
      if (!course) continue;
      docs.push({
        id: `saved:${q.course}:${q.ts}`,
        kind: 'saved',
        course: q.course,
        courseTitle: course.title,
        moduleId: moduleIdFor(course, q.lesson),
        lesson: q.lesson,
        title: q.name,
        text: String(q.sql || ''),
      });
    }
  }

  return docs;
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function search(index: SearchDoc[], query: string, limit = 25): SearchHit[] {
  const raw = String(query || '').trim();
  if (!raw) return [];
  const q = raw.toLowerCase();
  const tokens = q.split(/\s+/).filter(Boolean);
  const hits: SearchHit[] = [];

  for (const doc of index) {
    const title = doc.title.toLowerCase();
    const body = doc.text.toLowerCase();
    let score = KIND_WEIGHT[doc.kind];
    let matchedAll = true;
    for (const token of tokens) {
      const inTitle = title.includes(token);
      const inBody = body.includes(token);
      if (!inTitle && !inBody) {
        matchedAll = false;
        break;
      }
      score += inTitle ? 6 : 1;
      if (title.startsWith(token)) score += 2;
      if (new RegExp(`\\b${escapeRegExp(token)}`).test(title)) score += 1;
    }
    if (!matchedAll) continue;
    if (title.includes(q)) score += 8;
    if (body.includes(q)) score += 3;
    hits.push({ doc, score });
  }

  hits.sort((a, b) => b.score - a.score || a.doc.lesson - b.doc.lesson || a.doc.id.localeCompare(b.doc.id));
  return hits.slice(0, limit);
}

/** Group hits by kind for the results panel, preserving rank within a group. */
export function groupHits(hits: SearchHit[]): Array<{ kind: SearchKind; hits: SearchHit[] }> {
  const order: SearchKind[] = ['lesson', 'exercise', 'section', 'objective', 'bookmark', 'note', 'saved'];
  return order
    .map((kind) => ({ kind, hits: hits.filter((h) => h.doc.kind === kind) }))
    .filter((g) => g.hits.length > 0);
}

export const SEARCH_KIND_LABEL: Record<SearchKind, string> = {
  lesson: 'Chapters',
  exercise: 'Exercises',
  section: 'Sections',
  objective: 'Objectives',
  bookmark: 'Bookmarks',
  note: 'Notes',
  saved: 'Saved queries',
};
