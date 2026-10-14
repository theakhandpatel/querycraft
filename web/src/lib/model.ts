import { COURSES, MODULES } from '../data/curriculum';
import type { Block, Course, CourseModule, Progress, UiState } from '../types';

export interface FoundExercise {
  course: Course;
  module: CourseModule;
  index: number;
  block: Block;
}

export interface OutlineGroup {
  kind: 'concept' | 'practice';
  title: string;
  items: Block[];
  exIds: string[];
}

/* ---------- catalog helpers (pure; progress passed in explicitly) ---------- */
export function courseById(id: string): Course | null {
  return COURSES.find((c) => c.id === id) || null;
}
export function currentCourse(state: UiState): Course {
  return courseById(state.selectedCourseId) || COURSES[0];
}
export function activeModules(course?: Course | null): CourseModule[] {
  return (course && course.modules) || COURSES[0].modules || MODULES;
}
export function builtCourses(): Course[] {
  return COURSES.filter((c) => c.built);
}
export function courseShort(c: Course | null | undefined): string {
  if (!c) return '';
  return c.title.split(' ').slice(0, 2).join(' ');
}
export function exerciseIdsOfModule(m: CourseModule): string[] {
  return (m.blocks || []).filter((b) => b.type === 'exercise' && !!b.id).map((b) => b.id as string);
}
export function courseExerciseIds(c: Course | null | undefined): string[] {
  const ids: string[] = [];
  (c && c.modules ? c.modules : []).forEach((m) => exerciseIdsOfModule(m).forEach((id) => ids.push(id)));
  return ids;
}
export function courseExerciseTotal(c: Course | null | undefined): number {
  return courseExerciseIds(c).length;
}
export function moduleExerciseBlocks(m: CourseModule): Block[] {
  return (m.blocks || []).filter((b) => b.type === 'exercise');
}
export function allModules(): CourseModule[] {
  const out: CourseModule[] = [];
  COURSES.forEach((c) => (c.modules || []).forEach((m) => out.push(m)));
  return out;
}
export function findExercise(id: string): FoundExercise | null {
  let out: FoundExercise | null = null;
  COURSES.forEach((c) =>
    (c.modules || []).forEach((m, mi) =>
      (m.blocks || []).forEach((b) => {
        if (b.type === 'exercise' && b.id === id) out = { course: c, module: m, index: mi, block: b };
      }),
    ),
  );
  return out;
}
export function moduleIndexOfExercise(id: string): number | null {
  const f = findExercise(id);
  return f ? f.index : null;
}
export function eventTitle(id: string): string {
  let prompt: string | null = null;
  allModules().forEach((m) =>
    (m.blocks || []).forEach((b) => {
      if (b.type === 'exercise' && b.id === id) prompt = b.prompt ?? null;
    }),
  );
  let t = (prompt || id).split('.')[0].trim();
  if (t.length > 58) t = t.slice(0, 55).trim() + '…';
  return t.charAt(0).toUpperCase() + t.slice(1);
}

/* Group a chapter's blocks into concept/practice sections (ported verbatim). */
export function chapterOutline(m: CourseModule): OutlineGroup[] {
  const groups: OutlineGroup[] = [];
  let cur: OutlineGroup | null = null;
  (m.blocks || []).forEach((b) => {
    const startNew =
      !!b.section ||
      cur === null ||
      (b.type === 'exercise' && cur.kind !== 'practice') ||
      (b.type !== 'exercise' && cur.kind === 'practice');
    if (startNew) {
      cur = {
        kind: b.type === 'exercise' ? 'practice' : 'concept',
        title: b.section || (b.type === 'exercise' ? `Exercise ${String(b.id || '').replace('ex.', '')}` : 'Concept'),
        items: [],
        exIds: [],
      };
      groups.push(cur);
    }
    const group = cur as OutlineGroup;
    group.items.push(b);
    if (b.type === 'exercise' && b.id) group.exIds.push(b.id);
  });
  return groups;
}

/* ---------- progress-dependent derivations ---------- */
export function coursePassed(c: Course, progress: Progress): number {
  return courseExerciseIds(c).filter((id) => progress.passed[id]).length;
}
export function courseAccepted(c: Course, progress: Progress): number {
  return courseExerciseIds(c).filter((id) => progress.accepted[id]).length;
}
export function courseDoneCount(c: Course, progress: Progress): number {
  return (c.modules || []).filter((m) => progress.done[m.id]).length;
}
export function courseAttempts(c: Course, progress: Progress): number {
  return (progress.events || []).filter((e) => {
    const f = findExercise(e.id);
    return !!f && f.course.id === c.id && (e.kind === 'passed' || e.kind === 'failed');
  }).length;
}
export function lessonPercent(c: Course | null | undefined, progress: Progress): number {
  const course = c || COURSES[0];
  const total = courseExerciseTotal(course) || 1;
  const chapters = (course.modules || []).length || 1;
  return Math.min(
    100,
    Math.round((courseDoneCount(course, progress) / chapters) * 60 + (coursePassed(course, progress) / total) * 40),
  );
}
export function nextChapterIndex(c: Course | null | undefined, progress: Progress): number {
  const ms = (c && c.modules) || [];
  for (let i = 0; i < ms.length; i++) if (!progress.done[ms[i].id]) return i;
  return 0;
}
