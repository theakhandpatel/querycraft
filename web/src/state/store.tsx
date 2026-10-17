/*
 * The single app provider. It is the largest file in web/src, and a review
 * flagged that: ~90 members spanning auth, the hash router, progress
 * persistence + IndexedDB sync, the AI build orchestration, catalog mutation
 * and theme. That is a real maintainability note, not a bug, and the split
 * (progress / router / catalog) is deliberately deferred: it is a large,
 * regression-prone refactor of the one module every screen imports, and every
 * behaviour here is already covered by the audit suites. This banner exists so
 * the next reader sees the map instead of rediscovering it.
 *
 * Concerns, in this order:
 *   1. auth + session (signup/login/logout/onUnauthorized)
 *   2. the hash router (applyHash/hashFromState/showScreen)
 *   3. progress: load, mutate, owner-scoped persist, gateway sync, IndexedDB
 *   4. the AI build orchestration (draft → outline → chapters → activate)
 *   5. catalog mutation (notes, bookmarks, saved queries, workbook)
 *   6. theme + motion
 */
import React, { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';import { createApi, readAuthToken, readAuthUser } from '../api/index';
import { COURSES, MODULES } from '../data/curriculum';
import {
  activeModules as activeModulesOf,
  courseById,
  courseShort,
  currentCourse as currentCourseOf,
  eventTitle,
  findExercise,
  lessonPercent as lessonPercentOf,
  moduleExerciseBlocks,
  nextChapterIndex as nextChapterIndexOf,
} from '../lib/model';
import { buildExportBundle, collectDrafts, downloadExport } from '../lib/exportBundle';
import { applyImport, describeImport, readImport, type ImportOutcome } from '../lib/importBundle';
import { AUTHORING_SCREENS, HOME_HASH, hashForState, isRouteHash, routeTarget } from '../lib/route';
import type { RouteTarget } from '../lib/route';
import { datasetFor } from '../lib/dataset';
import { dialectFor } from '../lib/dialect';
import { datasetVersion } from '../api/dataset';
import { scrollToNode } from '../lib/scroll';
import { clearRun, readRun, writeRun } from '../lib/runResume';
import { jobIsRunning } from '../api/ai';
import type { AiAvailability, AiOutcome } from '../api/aiContract';
import type { AiCourseRecord } from '../api/aiContract';
import { courseFromPlan, reportFromOutcome, type DraftReport } from '../lib/draft';
import {
  emptyProgress,
  isSampleProgress,
  migrateProgress,
  newAttemptId,
  rebuildProgressFromEvents,
  seedProgress,
  stampFor,
} from '../lib/progress';
import type {
  ApiLogEntry,
  AuthSession,
  Block,
  ContentSection,
  Course,
  CourseModule,
  EngineCapabilities,
  EventKind,
  ExecutionApi,
  LoginInput,
  ModalName,
  PromptRequest,
  Progress,
  ProgressEvent,
  Screen,
  SignupInput,
  ThemeName,
  UiState,
  UserRole,
} from '../types';

const THEME_KEY = 'qc.theme';
const MOTION_KEY = 'qc.motion';
/* Progress storage is owner-scoped: the key carries the account id, so two
   learners signed in on one browser can never read each other's workspace out
   of localStorage. A single shared `qc.progress` key was exactly that leak. */
const PROG_KEY = 'qc.progress';
/** The key an anonymous browser — the offline local workspace, or a signed-out
    gateway tab — writes under. The local workspace's session id is `local`, so
    the two agree. */
const LOCAL_PROGRESS_ID = 'local';
function progressKeyFor(userId: string | null | undefined): string {
  return `${PROG_KEY}.${userId || LOCAL_PROGRESS_ID}`;
}
/** Per-course ceiling on kept queries, so the workbook stays a workbook. */
const SAVED_PER_COURSE = 50;
const STATE_KEY = 'qc.state';

/**
 * The PGlite (offline) adapter runs PostgreSQL in the page with no auth server
 * behind it, so there is nothing to sign in to. The app runs as this local
 * workspace instead — an explicitly local identity, never a server session, so
 * it is honest about the offline mode rather than fabricating a login. The
 * gateway is what makes real accounts (and owner-scoped progress) possible.
 */
const LOCAL_SESSION: AuthSession = {
  user: {
    id: 'local',
    email: 'local@querycraft',
    name: 'Local workspace',
    role: 'admin',
    createdAt: '',
  },
  token: '',
  expiresAt: '',
};

function loadJSON<T>(k: string, fallback: T): T {
  try {
    const v = localStorage.getItem(k);
    return v == null ? fallback : JSON.parse(v);
  } catch {
    return fallback;
  }
}
function saveJSON(k: string, v: unknown): void {
  try {
    localStorage.setItem(k, JSON.stringify(v));
  } catch {}
}

/** A short course blurb the draft owns, derived from the learner's own goal.
    It is never the built-in course's blurb. */
function blurbFromGoal(goal: string): string {
  const g = goal.trim().replace(/\s+/g, ' ');
  if (!g) return 'A course shaped around the skill you want to build, with exercises that run on real PostgreSQL.';
  const short = g.length > 150 ? g.slice(0, 147).trimEnd() + '…' : g;
  return `Built from your goal: ${short}`;
}

/** Clone the reference curriculum into a draft course's own modules, giving
    every module and exercise a course-scoped id so progress and attempts never
    leak between the draft and the built-in course. Each block is copied
    shallowly, so `checks[].test` functions survive by reference. */
function draftModulesFor(suffix: string): CourseModule[] {
  return MODULES.map((m) => ({
    ...m,
    id: `${m.id}${suffix}`,
    objectives: [...m.objectives],
    blocks: (m.blocks || []).map((b) => (b.id ? { ...b, id: `${b.id}${suffix}` } : { ...b })),
  }));
}

/** Everything screens receive from useStore(). */
export interface StoreValue {
  COURSES: Course[];
  MODULES: CourseModule[];
  progress: Progress;
  uiState: UiState;
  course: Course;
  draftCourse: Course | null;
  modules: CourseModule[];
  pct: number;
  activeLesson: number;

  api: ExecutionApi;
  apiLog: ApiLogEntry[];
  apiCount: number;
  /** Where SQL runs and whether a runaway query can be stopped (design §5.2). */
  engineCaps: EngineCapabilities | null;
  /** True only when the gateway's seeded default administrator can sign in with
      an empty password — a loopback-only local install. The sign-in hint reads
      this and no other reason, so a real deployment never advertises a blank
      password. False for the offline adapters and whenever the probe fails. */
  adminPasswordEmpty: boolean;
  /** Whether the AI lab can run, and why not when it cannot (design §11.2).
      Read once per session; null until the first answer arrives. */
  aiAvailability: AiAvailability | null;
  latency: boolean;
  setLatency: React.Dispatch<React.SetStateAction<boolean>>;
  setLibraryFilter: (f: string) => void;

  /* Accounts. `session` is null when signed out (the app then shows the auth
     screen in gateway mode); `role` gates the authoring surfaces. */
  session: AuthSession | null;
  authReady: boolean;
  authNotice: string | null;
  role: UserRole;
  signup: (input: SignupInput) => Promise<void>;
  login: (input: LoginInput) => Promise<void>;
  logout: () => Promise<void>;

  screen: Screen;
  showScreen: (id: Screen) => void;
  /** The challenge the lab is showing, when a link (`#/lab/:id`) named one. */
  challengeId: string | null;
  /** Open the challenge lab, optionally on one registered challenge. */
  openChallenge: (id?: string | null) => void;
  reviewTab: string;
  setReviewTab: React.Dispatch<React.SetStateAction<string>>;
  tablesOpen: boolean;
  setTablesOpen: React.Dispatch<React.SetStateAction<boolean>>;
  /** The chapter-list drawer on the lesson screen (the old left column). */
  chaptersOpen: boolean;
  setChaptersOpen: React.Dispatch<React.SetStateAction<boolean>>;
  /** The navigation drawer below 760px, where the top-bar links do not fit. */
  navOpen: boolean;
  setNavOpen: React.Dispatch<React.SetStateAction<boolean>>;
  modal: ModalName;
  openModal: (m: ModalName) => void;
  closeModal: () => void;
  toastMsg: string | null;
  toast: (msg: string) => void;
  promptRequest: PromptRequest | null;
  requestPrompt: (req: PromptRequest) => void;
  closePrompt: () => void;

  theme: ThemeName;
  effectiveTheme: 'light' | 'dark';
  setTheme: (m: ThemeName) => void;
  cycleTheme: () => void;
  motion: boolean;
  setMotion: () => void;

  openLessonIn: (courseId: string, index?: number) => void;
  openLesson: (i?: number) => void;
  selectCourse: (id: string) => void;
  enrollCourse: (id: string) => void;
  resumeCourse: () => void;
  startLearning: () => void;
  stepLesson: (d: number) => void;
  markLessonDone: () => void;
  courseShort: (c: Course | null | undefined) => string;

  saveProgress: (next?: Progress) => void;
  recordEvent: (id: string, kind: EventKind, label?: string) => void;
  addNote: (text: string) => void;
  /** Design §10: keep a query (a result or a revealed solution) in the workbook. */
  saveQuery: (sql: string, block?: Block) => void;
  /** Remove a kept query by its timestamp. */
  removeSaved: (ts: number) => void;
  /** Design §10: bookmark one section of a chapter, or clear the bookmark that
      is already there, so the workbook can link the learner back to it. */
  toggleBookmark: (title: string, lesson: number, section: number) => void;
  /** Remove a workbook item (note or bookmark) by its timestamp. */
  removeWorkbookItem: (ts: number) => void;
  /** Open the chapter a workbook item belongs to and jump to its section. */
  openBookmark: (lesson: number, section?: number) => void;
  /** Design §10: open a search result's chapter, and scroll to the cell or
      section it names when the hit has one. */
  openSearchHit: (hit: { course: string; moduleId: string; lesson: number; section?: number; exercise?: string }) => void;
  /** True when the active course already bookmarks this chapter section. */
  isBookmarked: (lesson: number, section: number) => boolean;
  /** Add measured active-study milliseconds to a course's total. */
  addStudyTime: (courseId: string, ms: number) => void;
  /** Opt in to the generated demo workspace (fabricated history, for tours). */
  loadSampleProgress: () => void;
  clearProgress: () => void;
  /** Download the versioned JSON export (design §8.3). Returns the filename. */
  exportData: () => string;
  /** Read and merge a versioned JSON export (design §8.3/M2 step 7). Validates
      the whole document first; a refusal changes nothing. */
  importData: (text: string) => ImportOutcome;

  catalogVersion: number;
  /** Chapter reading content, keyed by module id, read from the database. */
  content: Record<string, ContentSection[]>;
  moveLesson: (i: number, dir: number) => void;
  saveCourseEdits: (edits: { title?: string; goal?: string; context?: string; dialect?: string }) => void;
  saveLessonEdits: (edits: { title?: string; summary?: string }) => void;
  applyExtension: (kind: string, prompt?: string) => void;
  addLesson: (args: { title: string; goal?: string; position?: string }) => void;
  addSection: (title: string) => void;

  goalInput: string;
  setGoalInput: React.Dispatch<React.SetStateAction<string>>;
  reviewGoal: string;
  setReviewGoal: React.Dispatch<React.SetStateAction<string>>;
  courseSubtitle: string;
  setCourseSubtitle: React.Dispatch<React.SetStateAction<string>>;
  contextState: string;
  setContextState: React.Dispatch<React.SetStateAction<string>>;
  /** Returns the draft title, or undefined when the caller may not author. */
  /** Records the ask. The course itself is produced by the build that follows:
      `adoptGeneratedCourse` when a provider is configured, `useAuthoredDraft`
      when one is not. */
  submitBuild: (goal: string, context?: { level?: string; source?: string; requireApproval?: boolean }) => void;
  /** What the learner said about their level and source material, kept so the
      generation request can carry it, plus whether they asked to approve the
      outline before the chapters are written. */
  buildContext: { level?: string; source?: string; requireApproval?: boolean };
  /** Open the built-in curriculum as the draft, labelled `authored`. */
  useAuthoredDraft: (goal: string) => string | undefined;
  /** Adopt a gateway-generated course. Returns null when the run did not
      activate — the report is recorded either way — so a screen never offers a
      course that was not verified. */
  adoptGeneratedCourse: (outcome: AiOutcome, goal: string) => { id: string; title: string } | null;
  /** What the last build measured, or null when none has run. */
  draftReport: DraftReport | null;
  setDraftReport: React.Dispatch<React.SetStateAction<DraftReport | null>>;

  /* M8. The gateway owns the learner's generated courses, so the app reads them
     rather than remembering them in a tab. `storedCourses` is that list; the
     same courses are mirrored into `COURSES` so the router, the lesson page and
     the search index can resolve a course id the way they always have. */
  storedCourses: Course[];
  /** True while a course the fragment named is being fetched from the gateway,
      so the shell can wait for it instead of painting the wrong course. */
  courseResolving: boolean;
  /** Remove one of the learner's own courses through the gateway, and from the
      catalog. Resolves false when the gateway refused and nothing changed. */
  deleteCourse: (id: string) => Promise<boolean>;
  /** Register a course the gateway stored (used while one is still being
      written), so the catalog and every screen see it. */
  adoptStoredCourse: (rec: AiCourseRecord) => Course;

  /* M8. The background run this tab was watching, recovered after a reload.
     `null` when there is nothing to re-attach to — which is the normal case,
     not an error. A run outlives the page that started it, so the id is kept
     locally and every fact about the run is read back from the gateway. */
  resumeRun: { jobId: string; goal: string } | null;
  /** Remember a run as this tab's own, so a reload can come back to it. */
  runStarted: (jobId: string, goal: string) => void;
  /** Forget a run once it has finished, been refused, or gone missing. */
  runSettled: (jobId: string) => void;

  moduleExerciseBlocks: (m: CourseModule) => Block[];
}

const StoreContext = createContext<StoreValue | null>(null);

export const SCREEN_CRUMBS: Record<Screen, string> = {
  home: 'Home',
  course: 'My learning',
  library: 'Library',
  create: 'Create a course',
  building: 'Building course',
  review: 'Draft studio',
  manage: 'Manage',
  lesson: 'Chapter',
  workbook: 'Workbook',
  'workbook-progress': 'Progress',
  'challenge-lab': 'Challenge lab',
  'appearance-settings': 'Appearance',
};
export const SCREEN_NAV: Record<Screen, string> = {
  home: 'home',
  course: 'course',
  library: 'library',
  create: 'create',
  building: 'create',
  review: 'create',
  manage: 'manage',
  lesson: 'course',
  workbook: 'workbook',
  'workbook-progress': 'progress',
  'challenge-lab': 'lab',
  'appearance-settings': 'appearance',
};

/**
 * First-run state is empty, full stop.
 *
 * This used to call seedProgress(), which fabricated weeks of history — passed
 * exercises, notes, bookmarks, a streak — for every new workspace. With a real
 * gateway behind the API that made a live app look like a mock: checkmarks and
 * "60%" nobody earned. Sample data is now opt-in from Appearance.
 */
function initialProgress(userId: string | null | undefined): Progress {
  const key = progressKeyFor(userId);
  const raw = loadJSON<unknown>(key, null);
  if (raw && !isSampleProgress(raw)) {
    const migrated = migrateProgress(raw);
    if (migrated) return migrated;
  }
  /* Nothing usable on disk: a missing blob, generated sample data, or a
     snapshot too old to migrate. Start empty and overwrite the stale row so
     every boot does not have to decide to ignore it again. */
  const fresh = emptyProgress();
  saveJSON(key, fresh);
  return fresh;
}

export function StoreProvider({ children }: { children: React.ReactNode }) {
  /* The fragment is the entry point. A deep link, a reload or a paste into the
     address bar has to land on the screen it names, so the first render reads
     the URL instead of always starting at Home and correcting afterwards. Every
     navigation after that goes through `hashchange`. */
  const [entry] = useState<RouteTarget>(() => routeTarget(typeof window === 'undefined' ? HOME_HASH : window.location.hash));
  const [uiState, setUiState] = useState<UiState>(() => {
    const s = loadJSON<UiState | null>(STATE_KEY, null) || { selectedCourseId: 'sql-analytics', libraryFilter: 'all' };
    if (!s.libraryFilter) s.libraryFilter = 'all';
    /* A chapter link names its course as well as its chapter, and opening a
       chapter from inside the app selects that course; a link resolves the
       same way rather than inventing an enrolment. */
    if (entry.courseId) s.selectedCourseId = entry.courseId;
    return s;
  });
  /* initialProgress reads AND writes (it seeds the stored snapshot), so it is
     evaluated once here rather than in two separate state initializers. */
  const [progress, setProgress] = useState<Progress>(() => initialProgress(readAuthUser()));
  const [screen, setScreen] = useState<Screen>(entry.screen);
  const [challengeId, setChallengeId] = useState<string | null>(entry.challengeId);
  const [activeLesson, setActiveLesson] = useState(() => {
    if (entry.chapterIndex != null) return entry.chapterIndex;
    return nextChapterIndexOf(entry.courseId ? courseById(entry.courseId) : COURSES[0], progress);
  });
  const [theme, setThemeState] = useState<ThemeName>(() => loadJSON<ThemeName>(THEME_KEY, 'light'));
  const [motion, setMotionState] = useState<boolean>(() => loadJSON<boolean>(MOTION_KEY, false));
  const [reviewTab, setReviewTab] = useState('overview');
  const [tablesOpen, setTablesOpen] = useState(false);
  const [chaptersOpen, setChaptersOpen] = useState(false);
  const [navOpen, setNavOpen] = useState(false);
  const [modal, setModal] = useState<ModalName>(null);
  const [toastMsg, setToastMsg] = useState<string | null>(null);
  const [promptRequest, setPromptRequest] = useState<PromptRequest | null>(null);
  const [apiLog, setApiLog] = useState<ApiLogEntry[]>([]);
  const [apiCount, setApiCount] = useState(0);
  const [latency, setLatency] = useState(true);
  const [catalogVersion, setCatalogVersion] = useState(0);
  const [content, setContent] = useState<Record<string, ContentSection[]>>({});
  const [goalInput, setGoalInput] = useState('');
  const [reviewGoal, setReviewGoal] = useState('Learn to answer realistic product and business questions with SQL, then explain the reasoning behind your query.');
  const [courseSubtitle, setCourseSubtitle] = useState('A practical progression from multi-table queries to analytical SQL and query reasoning.');
  const [contextState, setContextState] = useState('AI-generated');
  /* The most recently built draft, so review/building can target it without
     reaching for COURSES[0]. It is added to COURSES for the catalog helpers. */
  const [draftCourseId, setDraftCourseId] = useState<string | null>(null);
  /* What the last build actually measured. Null means no build has run in this
     session, and every screen that would show a validation number reads this
     instead of printing one of its own. */
  const [draftReport, setDraftReport] = useState<DraftReport | null>(null);
  const [buildContext, setBuildContext] = useState<{ level?: string; source?: string; requireApproval?: boolean }>({});
  /* The learner's own generated courses, as the gateway stores them. Read on
     sign-in and after a generation, never invented: the offline adapters keep
     no such store, so the list stays empty there rather than claiming the
     learner has no courses. */
  const [storedCourses, setStoredCourses] = useState<Course[]>([]);
  const storedRef = useRef<Course[]>([]);
  /* A background run this tab started and had not finished watching when the
     page went away. Read back on boot: the run itself never stopped, only the
     view of it did. */
  const [resumeRun, setResumeRun] = useState<{ jobId: string; goal: string } | null>(null);
  /* A course id a fragment named that the catalog does not have. It may be a
     stored course the gateway owns, so it is fetched before the screen paints:
     a `#/course/{id}` link used to fall back to the first built-in course and
     rewrite the URL, which is how a generated course "vanished" on reload. */
  const [pendingCourse, setPendingCourse] = useState<{ id: string; chapterId: string | null } | null>(() =>
    entry.courseUnresolved && entry.courseId ? { id: entry.courseId, chapterId: entry.chapterId ?? null } : null
  );
  const [session, setSession] = useState<AuthSession | null>(null);
  /* The session as the callbacks below need it: they are stable so a screen can
     call them once, and must not close over the session of the first render. */
  const sessionRef = useRef<AuthSession | null>(null);
  sessionRef.current = session;
  const [authReady, setAuthReady] = useState(false);
  const [authNotice, setAuthNotice] = useState<string | null>(null);

  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  /* Hydration guards. `hydrated` stops a second pull; `dirty` records that the
     learner changed something before the pull resolved, so a late server
     snapshot can never clobber live edits. */
  const hydratedRef = useRef(false);
  const dirtyRef = useRef(false);
  const onLog = useCallback((logArr: ApiLogEntry[], count: number) => {
    setApiLog(logArr.slice());
    setApiCount(count);
  }, []);
  /* A 401 with AUTH_REQUIRED from any gateway request means the token is gone or
     expired: drop the session and send the learner back to sign-in. The http
     layer fires this; the offline adapters never do. */
  const onUnauthorized = useCallback(() => {
    setSession(null);
    setAuthNotice('Your session expired. Sign in to continue.');
    setScreen('home');
  }, []);
  const api = useMemo(() => createApi({ onLog, onUnauthorized }), [onLog, onUnauthorized]);
  useEffect(() => {
    if (api.config) api.config.latency = latency;
  }, [api, latency]);

  /* Restore the session once, before the app decides what to render.
   *
   * The offline engine has no auth server, so it runs as a local workspace. The
   * gateway reports the current account from the stored token; no account means
   * the auth screen.
   * `me()` resolves to null rather than failing on a missing/expired token, so
   * this is a plain "who am I" check. */
  useEffect(() => {
    let alive = true;
    const restore = async () => {
      if (api.mode === 'pglite') {
        if (alive) {
          setSession(LOCAL_SESSION);
          setAuthReady(true);
        }
        return;
      }
      let user = null;
      try {
        user = await api.me();
      } catch {
        user = null;
      }
      if (!alive) return;
      setSession(user ? { user, token: readAuthToken() ?? '', expiresAt: '' } : null);
      setAuthReady(true);
    };
    void restore();
    return () => {
      alive = false;
    };
  }, [api]);

  /* ---------- theme + motion ---------- */
  const effectiveTheme = theme === 'system' ? (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : theme;
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', effectiveTheme);
  }, [effectiveTheme]);
  useEffect(() => {
    const mq = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
    if (!mq) return;
    const fn = () => {
      if (loadJSON<ThemeName>(THEME_KEY, 'light') === 'system') document.documentElement.setAttribute('data-theme', mq.matches ? 'dark' : 'light');
    };
    mq.addEventListener ? mq.addEventListener('change', fn) : mq.addListener(fn);
    return () => (mq.removeEventListener ? mq.removeEventListener('change', fn) : mq.removeListener(fn));
  }, []);
  useEffect(() => {
    if (motion) document.documentElement.setAttribute('data-motion', 'reduced');
    else document.documentElement.removeAttribute('data-motion');
  }, [motion]);

  const toast = useCallback((msg: string) => {
    setToastMsg(msg);
    if (toastTimer.current !== null) clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToastMsg(null), 2600);
  }, []);

  /* Chapter content lives in the app database; load it once when the API is up.
     The module list is derived from the courses rather than named here: a
     hand-written list silently omitted a whole course the moment one was added,
     and the lessons then fell back to the bundled prose with no error to notice.

     One module failing must not blank the other fifty-one. The first boot pays
     for the WASM download and the whole seed, and a rejected `Promise.all` left
     `content` empty for the life of the tab: every lesson then rendered the short
     curriculum prose, and nothing on screen said that the authored chapter had
     never loaded. Settle each module on its own, retry the ones that failed, and
     say so out loud when content is still missing. */
  useEffect(() => {
    let alive = true;
    const ids = COURSES.flatMap((c) => c.modules ?? []).map((m) => m.id);
    const read = async (id: string): Promise<{ id: string; docs?: ContentSection[]; error?: unknown }> => {
      try {
        return { id, docs: await api.getContent(id) };
      } catch (error) {
        return { id, error };
      }
    };

    const load = async () => {
      const first = await Promise.all(ids.map(read));
      /* A boot race that drops a read is transient — the database is still
         seeding — so the modules that missed get one more pass. */
      const missed = first.filter((r) => r.docs === undefined).map((r) => r.id);
      const second = missed.length > 0 ? await Promise.all(missed.map(read)) : [];
      if (!alive) return;
      const got = [...first, ...second].filter((r) => (r.docs?.length ?? 0) > 0) as { id: string; docs: ContentSection[] }[];
      setContent(Object.fromEntries(got.map((r) => [r.id, r.docs])));
      const arrived = new Set(got.map((r) => r.id));
      const missing = ids.filter((id) => !arrived.has(id));
      if (missing.length === 0) return;
      console.warn(`[querycraft] chapter content missing for ${missing.length} of ${ids.length} modules`, missing, first.find((r) => r.error)?.error);
      if (missing.length === ids.length && ids.length > 0) {
        /* Nothing loaded at all: a lesson that quietly shows the fallback prose
           would be a mock, so the reader is told the chapter is unavailable. */
        toast('Chapter reading content could not be loaded, so lessons show the chapter summary instead.');
      }
    };

    void load();
    return () => {
      alive = false;
    };
  }, [api, toast]);


  const saveUiState = useCallback((next: UiState) => {
    setUiState(next);
    saveJSON(STATE_KEY, next);
  }, []);

  /* Read through a ref so this stays identity-stable. The study timer commits
     on an interval, and a callback that changed on every progress write would
     restart the timer effect (and its cleanup flush) on every tick. */
  const progressRef = useRef(progress);
  useEffect(() => {
    progressRef.current = progress;
  }, [progress]);

  /* Progress is owner-scoped. The owner changes on signup, login, logout, a
     401, and session restore, and the in-memory snapshot has to change with it
     — otherwise a learner could see the previous account's passes for a frame
     and the next write would upload them into the new account's row. This is a
     layout effect, not a passive one: it runs before the browser paints, so the
     new owner's snapshot is in place for the first frame of the new session.
     `authReady` gates it so the initial render's snapshot (loaded from the
     stored account id) is not swapped away before the session has resolved. */
  const ownerId = session?.user.id ?? LOCAL_PROGRESS_ID;
  const progressOwnerRef = useRef<string>((readAuthUser() || LOCAL_PROGRESS_ID));
  useLayoutEffect(() => {
    if (!authReady || progressOwnerRef.current === ownerId) return;
    progressOwnerRef.current = ownerId;
    hydratedRef.current = false;
    dirtyRef.current = false;
    const next = initialProgress(ownerId);
    progressRef.current = next;
    setProgress(next);
  }, [authReady, ownerId]);

  const persistProgress = useCallback((next?: Progress) => {
    const p = next || progressRef.current;
    dirtyRef.current = true;
    saveJSON(progressKeyFor(progressOwnerRef.current), p);
    return p;
  }, []);

  /* Where SQL actually runs, as reported by the adapter itself (§5.2). Read
     once the engine has booted; until then the screen keeps its default copy
     rather than guessing. */
  const [engineCaps, setEngineCaps] = useState<EngineCapabilities | null>(null);
  useEffect(() => {
    let alive = true;
    let tries = 0;
    const read = () => {
      api
        .capabilities()
        .then((caps) => {
          if (!alive || !caps) {
            if (alive && tries++ < 10) setTimeout(read, 700);
            return;
          }
          setEngineCaps(caps);
        })
        .catch(() => {
          /* no capability to report: the screen keeps its default wording */
        });
    };
    read();
    return () => {
      alive = false;
    };
    /* `session` is a dependency, not a detail. The gateway's answer to "can I
       write?" is about the gateway; whether *this* client can is a function of
       the session, and the session arrives after the first probe. Reading the
       capabilities once at boot left a signed-in learner on the read-only
       answer: the Analyze button stayed disabled with "sign in on the gateway"
       beside it, while the learner *was* signed in. Re-probing on a session
       change costs one request per sign-in and removes a whole class of that
       staleness. */
  }, [api, session]);

  /* Whether the sign-in screen may show its local-admin hint. Read from the
     gateway's own /health answer, so the hint appears only when the seeded
     administrator really does have an empty password AND the gateway is on a
     loopback bind (the gateway refuses to start otherwise). The offline
     adapters report false, so the hint is a gateway fact, not a guess. One read
     at boot is enough: it cannot change while this page is open. */
  const [adminPasswordEmpty, setAdminPasswordEmpty] = useState(false);
  useEffect(() => {
    let alive = true;
    api
      .adminPasswordEmpty()
      .then((empty) => {
        if (alive) setAdminPasswordEmpty(empty);
      })
      .catch(() => {
        /* adminPasswordEmpty never fails; a broken layer must not break boot,
           and the safe default is "hidden". */
      });
    return () => {
      alive = false;
    };
  }, [api]);

  /* Whether the AI lab can run, read once the session is known so the lab does
     not flash "unavailable" before the gateway has answered. aiAvailability()
     never rejects — an unreadable probe lands as an unavailable value with a
     reason — so this only has to guard against a stale write after unmount. */
  const [aiAvailability, setAiAvailability] = useState<AiAvailability | null>(null);
  useEffect(() => {
    let alive = true;
    api
      .aiAvailability()
      .then((availability) => {
        if (alive) setAiAvailability(availability);
      })
      .catch(() => {
        /* aiAvailability never rejects; a broken layer must not break boot. */
      });
    return () => {
      alive = false;
    };
  }, [api, session]);

  /* A durable write that fails has to be visible (M1: "a failed write must be
     surfaced"). The localStorage copy already holds the snapshot, so the
     learner keeps working — but the server/IndexedDB copy is now behind, and
     staying quiet about that would be the "it's a mock" tell all over again.
     One notice per session, so a browser that never allows storage cannot spam
     the toast on every keystroke. */
  const persistFailureNoticed = useRef(false);
  const reportPersistFailure = useCallback(
    (e: unknown) => {
      if (persistFailureNoticed.current) return;
      persistFailureNoticed.current = true;
      const code = (e as { code?: string } | null)?.code;
      toast(
        code === 'PERSISTENCE_FAILED'
          ? 'This browser blocked local storage, so your work is kept in this tab only.'
          : 'Could not sync your progress to the account. It is still saved in this browser.'
      );
    },
    [toast]
  );
  const syncProgress = useCallback(
    (p: Progress, courseId?: string) => {
      api.saveProgress(p, courseId ?? uiState.selectedCourseId).catch(reportPersistFailure);
    },
    [api, reportPersistFailure, uiState.selectedCourseId]
  );
  const syncEvent = useCallback(
    (evt: ProgressEvent) => {
      api.logEvent(evt).catch(reportPersistFailure);
    },
    [api, reportPersistFailure]
  );

  /* Learner data lives on the server, so pull it once per owner.
   *
   * Without this the UI was localStorage-only: the gateway recorded progress
   * that no client ever read back, so a second browser (or a cleared cache)
   * showed an empty workspace next to a full server row — the classic
   * "it's a mock" tell. The server snapshot now wins, but only if the learner
   * has not already touched something locally while the request was in flight.
   *
   * An absent snapshot on the gateway is authoritative, not "no opinion": it
   * means this account has never saved anything, so any local copy is cleared
   * rather than kept. Keeping it was how one learner's history bled into the
   * next account on a shared browser. The offline adapters are the opposite —
   * their store *is* the local one — so an empty read there leaves the local
   * workspace snapshot alone and it still survives a reload. */
  useEffect(() => {
    /* Progress and events are owner-scoped by the bearer token, so there is
       nothing to pull until the session is restored and a user is present. */
    if (!authReady || !session) return;
    if (hydratedRef.current) return;
    hydratedRef.current = true;
    let alive = true;
    const ownerId = session.user.id;
    api
      .getProgress(uiState.selectedCourseId)
      .then(({ ok, snapshot }) => {
        if (!alive || !ok) return;
        const hasSnapshot = !!snapshot && typeof snapshot === 'object';
        if (!hasSnapshot) {
          /* The gateway has no row for this account: clear the local snapshot
             so a shared browser never shows the previous account's work. A
             learner who changed something while the read was in flight keeps
             their edits; the server row is empty, not conflicting. */
          if (api.mode === 'http' && !dirtyRef.current) {
            const fresh = emptyProgress();
            progressRef.current = fresh;
            setProgress(fresh);
            saveJSON(progressKeyFor(ownerId), fresh);
          }
          return;
        }
        const remote = snapshot as Progress;
        /* A server row that only holds generated sample data is legacy residue
           from the old first-run seed; drop it instead of showing it as real. */
        if (isSampleProgress(remote)) {
          const fresh = emptyProgress();
          progressRef.current = fresh;
          setProgress(fresh);
          saveJSON(progressKeyFor(ownerId), fresh);
          syncProgress(fresh, uiState.selectedCourseId);
          return;
        }
        const migrated = migrateProgress(remote);
        if (!migrated || dirtyRef.current) return;
        const remoteProgress = migrated;
        progressRef.current = remoteProgress;
        setProgress(remoteProgress);
        saveJSON(progressKeyFor(ownerId), remoteProgress);
        /* A snapshot that needed upgrading must be written back, or the next
           boot would migrate it again from the same stale row. */
        if (remoteProgress !== remote) syncProgress(remoteProgress, uiState.selectedCourseId);
      })
      .catch(() => {
        /* offline or degraded gateway: keep the local snapshot */
      });
    return () => {
      alive = false;
    };
  }, [api, syncProgress, uiState.selectedCourseId, authReady, session]);

  const course = currentCourseOf(uiState);
  /* M5. The dataset the course runs on is part of the course, so the workspace
     it needs can be built while the learner is reading. Two effects follow from
     that: opening `plans` starts the 10 000-row fixture before the first Run
     asks for it, and opening a commerce course never does. */
  const courseDataset = datasetFor({ course });
  /* M6. The engine a chapter needs is the dialect dimension's answer, exactly as
     the dataset is the dataset dimension's: opening a SQLite course starts the
     SQLite WASM engine while the learner reads, and opening a PostgreSQL course
     never does. Warming the wrong engine is not merely wasted bandwidth — under
     `pglite` the two produce different SQL, so the statement would run somewhere
     the chapter never asked for. */
  const courseDialect = dialectFor({ course });
  useEffect(() => {
    api.warmUp(courseDataset, courseDialect);
  }, [api, courseDataset, courseDialect]);
  const draftCourse = draftCourseId ? COURSES.find((c) => c.id === draftCourseId) ?? null : null;
  const modules = activeModulesOf(course);
  const pct = lessonPercentOf(course, progress);
  /* The account's role, read once: the router gates the authoring screens with
     the same rule App renders behind. */
  const role: UserRole = session?.user.role ?? 'learner';
  /* M8. A fragment that names a course we have not resolved yet. The shell waits
     on the splash for it — but only for a learner the resolve can run for:
     signed out, the sign-in screen comes first and the link is picked up after
     the session arrives. */
  const courseResolving = pendingCourse !== null && authReady && session !== null;

  /* ---------- accounts ---------- */
  /* The layer stores the bearer token; the actions only track which account is
     signed in and let failures (bad credentials, taken email) reach the form. */
  const signup = useCallback(
    async (input: SignupInput) => {
      const next = await api.signup(input);
      hydratedRef.current = false;
      dirtyRef.current = false;
      setAuthNotice(null);
      setSession(next);
      setScreen('home');
    },
    [api]
  );
  const login = useCallback(
    async (input: LoginInput) => {
      const next = await api.login(input);
      hydratedRef.current = false;
      dirtyRef.current = false;
      setAuthNotice(null);
      setSession(next);
      setScreen('home');
    },
    [api]
  );
  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      /* clearing the local session matters more than the revoke round trip */
    }
    hydratedRef.current = false;
    dirtyRef.current = false;
    /* The offline engine has no session to end: stay on the local workspace. */
    setSession(api.mode === 'pglite' ? LOCAL_SESSION : null);
    setScreen('home');
  }, [api]);

  /* ---------- navigation ---------- */
  const showScreen = useCallback((id: Screen) => {
    setScreen(id);
    const ws = document.querySelector('.workspace');
    if (ws) ws.scrollTop = 0;
  }, []);
  /* The lab can be opened on a specific registered challenge; the fragment
     carries the id, so the same link reopens the same challenge. */
  const openChallenge = useCallback(
    (id: string | null = null) => {
      setChallengeId(id);
      showScreen('challenge-lab');
    },
    [showScreen]
  );
  const openLessonIn = useCallback(
    (courseId: string, index?: number) => {
      const c = COURSES.find((x) => x.id === courseId);
      if (!c || !c.modules) return;
      const next = { ...uiState, selectedCourseId: c.id };
      setUiState(next);
      saveJSON(STATE_KEY, next);
      const ms = c.modules;
      const idx = index == null ? nextChapterIndexOf(c, progress) : index;
      setActiveLesson(Math.max(0, Math.min(ms.length - 1, idx)));
      setScreen('lesson');
    },
    [uiState, progress]
  );
  const openLesson = useCallback((i?: number) => openLessonIn(uiState.selectedCourseId, i), [openLessonIn, uiState.selectedCourseId]);
  const selectCourse = useCallback(
    (id: string) => {
      const c = COURSES.find((x) => x.id === id);
      if (!c || !c.built) return;
      const next = { ...uiState, selectedCourseId: id };
      setUiState(next);
      saveJSON(STATE_KEY, next);
      setActiveLesson(nextChapterIndexOf(c, progress));
    },
    [uiState, progress]
  );
  const enrollCourse = useCallback(
    (id: string) => {
      const c = COURSES.find((x) => x.id === id);
      if (!c) return;
      if (!c.built) {
        toast('This course is a preview. Enrolling would generate it with the same pipeline.');
        return;
      }
      c.enrolled = true;
      toast(`Enrolled in "${c.title}". Progress is tracked separately.`);
      selectCourse(id);
      showScreen('course');
    },
    [selectCourse, showScreen, toast]
  );
  const resumeCourse = useCallback(() => openLessonIn(uiState.selectedCourseId, nextChapterIndexOf(course, progress)), [openLessonIn, uiState.selectedCourseId, course, progress]);
  const startLearning = useCallback(() => {
    const c = (draftCourseId && COURSES.find((x) => x.id === draftCourseId)) || COURSES[0];
    c.enrolled = true;
    selectCourse(c.id);
    showScreen('course');
    toast('Added to My learning.');
  }, [draftCourseId, selectCourse, showScreen, toast]);

  /* ---------- fragment ⇄ state (the router) ----------
   *
   * The URL is this app's history. Navigating inside the app writes the
   * fragment with `pushState` — which fires no event, so the two directions
   * cannot chase each other — and a `hashchange` (back, forward, a pasted link)
   * writes the state back. Both directions are guarded by the same comparison,
   * so a change that produces the fragment already in the address bar is a
   * no-op: a state change never re-emits the route it came from, and a
   * `hashchange` that matches the current screen changes nothing. */
  const uiStateRef = useRef(uiState);
  useEffect(() => {
    uiStateRef.current = uiState;
  }, [uiState]);

  /* Authoring is the admin surface; App turns a learner away from those
     screens, and the fragment follows the same rule so it never names a screen
     nobody is looking at. */
  const gated = useCallback(
    (target: RouteTarget): RouteTarget => (role !== 'admin' && AUTHORING_SCREENS.includes(target.screen) ? routeTarget(HOME_HASH) : target),
    [role]
  );

  /* state → URL.
     While a course the fragment named is still being fetched, the selected
     course is a placeholder and writing the fragment now would replace the
     learner's link with the placeholder's id. The effect waits for the real
     course: `pendingCourse` clears in the same commit that puts it in the
     catalog, and then this writes the fragment the learner already has. */
  useEffect(() => {
    if (pendingCourse !== null) return;
    const next = hashForState({ screen, courseId: course.id, chapterIndex: activeLesson, challengeId });
    const current = window.location.hash;
    if (current === next) return;
    /* A fragment that is not canonical (`#/nope`, a bare URL, `#/create` for a
       learner) is rewritten in place; only a real navigation adds an entry, so
       the back stack stays the learner's own. */
    if (gated(routeTarget(current)).hash === current) window.history.pushState(null, '', next);
    else window.history.replaceState(null, '', next);
  }, [screen, course.id, activeLesson, challengeId, gated, pendingCourse]);

  /* URL → state */
  const applyHash = useCallback(() => {
    const raw = window.location.hash;
    /* The shell's skip link moves the fragment to `#main`. That is an in-page
       anchor, not a route, so it is left to the browser. */
    if (!isRouteHash(raw)) return;
    const target = gated(routeTarget(raw));
    if (target.hash !== raw) window.history.replaceState(null, '', target.hash);
    if (target.courseId && target.courseId !== uiStateRef.current.selectedCourseId) {
      const nextState = { ...uiStateRef.current, selectedCourseId: target.courseId };
      uiStateRef.current = nextState;
      saveUiState(nextState);
    }
    if (target.chapterIndex != null) setActiveLesson(target.chapterIndex);
    else if (target.courseId) {
      const linked = courseById(target.courseId);
      if (linked) setActiveLesson(nextChapterIndexOf(linked, progressRef.current));
    }
    setChallengeId(target.challengeId);
    setScreen(target.screen);
    /* A fragment that names a course the catalog does not have keeps its id
       pending, so the gateway can answer for it before the router gives up. A
       link typed into the address bar is a `hashchange`, not a fresh load, so
       this is the only place that sees it. */
    /* The same link is re-resolved when the session is restored, so this only
       writes when the value actually changed: a fresh object holding the same id
       would be a new identity to React, and the resolver would fetch the course
       a second time for a link that never moved. */
    const pending = target.courseUnresolved && target.courseId ? { id: target.courseId, chapterId: target.chapterId ?? null } : null;
    setPendingCourse((prev) =>
      prev && pending && prev.id === pending.id && prev.chapterId === pending.chapterId ? prev : pending
    );
    const ws = document.querySelector('.workspace');
    if (ws) ws.scrollTop = 0;
  }, [gated, saveUiState]);

  useEffect(() => {
    window.addEventListener('hashchange', applyHash);
    window.addEventListener('popstate', applyHash);
    return () => {
      window.removeEventListener('hashchange', applyHash);
      window.removeEventListener('popstate', applyHash);
    };
  }, [applyHash]);

  /* The role is not known until the session is restored, so the fragment is
     resolved once more when it is: an authoring link that a learner may not open
     is turned away in place, and a link a signed-in learner opened still lands
     where it pointed. */
  useEffect(() => {
    if (!authReady) return;
    applyHash();
  }, [authReady, applyHash]);

  /* ---------- progress mutations ---------- */
  const saveProgress = useCallback(
    (next?: Progress) => {
      const p = next || progress;
      p.attemptsByDay = p.attemptsByDay || [];
      setProgress({ ...p });
      persistProgress(p);
      syncProgress(p, uiState.selectedCourseId);
    },
    [progress, persistProgress, syncProgress, uiState.selectedCourseId]
  );

  const recordEvent = useCallback(
    (id: string, kind: EventKind, label?: string) => {
      const f = findExercise(id);
      const evt = {
        ts: Date.now(),
        id,
        /* §8.2: one id per attempt, so two tries at one exercise are two rows. */
        attemptId: newAttemptId(),
        kind,
        title: label || eventTitle(id),
        course: f ? f.course.id : uiState.selectedCourseId,
        lesson: f ? f.index : activeLesson,
        /* §7.3: the attempt carries the versions it was made against. */
        ...stampFor(f?.block, datasetVersion),
      };
      const next = { ...progress, events: [...(progress.events || []), evt] };
      rebuildProgressFromEvents(next);
      setProgress(next);
      persistProgress(next);
      syncEvent(evt);
      syncProgress(next, evt.course);
    },
    [progress, activeLesson, uiState.selectedCourseId, persistProgress, syncEvent, syncProgress]
  );

  /* Measured study time. The lesson screen accumulates visible time and hands
     it over in chunks; this adds it to the running total for the course and
     syncs the snapshot so the gateway has the same number as the UI. */
  const addStudyTime = useCallback(
    (courseId: string, ms: number) => {
      if (!courseId || !(ms > 0)) return;
      const prev = progressRef.current;
      const total = (prev.timeMs?.[courseId] || 0) + ms;
      const next: Progress = { ...prev, timeMs: { ...(prev.timeMs || {}), [courseId]: total } };
      progressRef.current = next;
      setProgress(next);
      persistProgress(next);
      syncProgress(next, courseId);
    },
    [persistProgress, syncProgress]
  );

  const markLessonDone = useCallback(() => {
    const m = modules[activeLesson];
    if (!m) return;
    /* The learner's explicit signal lives in `manuallyDone`, not `done`: a
       rebuild of `done` from graded attempts runs on every event and would
       otherwise erase this. `done` is the union (see rebuildProgressFromEvents). */
    const manual = { ...(progress.manuallyDone || {}) };
    const nowDone = !(progress.done[m.id] || manual[m.id]);
    if (nowDone) manual[m.id] = true;
    else delete manual[m.id];
    const next = {
      ...progress,
      manuallyDone: manual,
      done: { ...progress.done, [m.id]: nowDone },
    };
    setProgress(next);
    persistProgress(next);
    /* Chapter completion is progress like any other mutation, so it must reach
       the gateway too — otherwise the server snapshot disagrees with the UI and
       the signal is lost on a different browser or a cleared cache. */
    syncProgress(next, uiState.selectedCourseId);
    toast(nowDone ? 'Chapter marked complete.' : 'Chapter marked incomplete.');
  }, [modules, activeLesson, progress, persistProgress, syncProgress, uiState.selectedCourseId, toast]);

  const stepLesson = useCallback(
    (d: number) => {
      const n = activeLesson + d;
      if (n < 0) return;
      if (n >= modules.length) {
        toast('End of the course. Nice work.');
        return;
      }
      openLessonIn(uiState.selectedCourseId, n);
    },
    [activeLesson, modules.length, openLessonIn, uiState.selectedCourseId, toast]
  );

  const addNote = useCallback(
    (text: string) => {
      if (!text) return;
      const c = course;
      const next: Progress = {
        ...progress,
        notes: [...(progress.notes || []), { course: c.id, lesson: activeLesson + 1, kind: 'note', ts: Date.now(), text }],
      };
      setProgress(next);
      persistProgress(next);
      syncProgress(next, c.id);
      toast('Note saved to your workbook.');
    },
    [course, progress, activeLesson, persistProgress, syncProgress, toast]
  );

  /* Design §10: a bookmark is a note that points at one section, plus a history
     event so the learner's own record shows when they flagged it. It is written
     as one snapshot so the note and the event cannot disagree. */
  const toggleBookmark = useCallback(
    (title: string, lesson: number, section: number) => {
      const c = course;
      const existing = (progress.notes || []).find(
        (n) => n.course === c.id && n.kind === 'bookmark' && n.lesson === lesson && n.section === section,
      );
      if (existing) {
        const next: Progress = {
          ...progress,
          notes: (progress.notes || []).filter((n) => n !== existing),
        };
        setProgress(next);
        persistProgress(next);
        syncProgress(next, c.id);
        toast('Bookmark removed.');
        return;
      }
      const ts = Date.now();
      const event: ProgressEvent = {
        ts,
        id: `bookmark.${c.id}.${lesson}.${section}`,
        attemptId: newAttemptId(),
        kind: 'bookmark',
        title: title || `Chapter ${lesson}`,
        course: c.id,
        lesson,
      };
      const next: Progress = {
        ...progress,
        notes: [...(progress.notes || []), { course: c.id, lesson, section, kind: 'bookmark', ts, text: title }],
        events: [...(progress.events || []), event],
      };
      setProgress(next);
      persistProgress(next);
      syncEvent(event);
      syncProgress(next, c.id);
      toast('Bookmarked for your workbook.');
    },
    [course, progress, persistProgress, syncEvent, syncProgress, toast]
  );

  /* Design §10: a kept query stores the SQL plus the versions it was written
     against, deduped on (course, exercise, sql) so pressing Keep twice is not
     two rows. The cap is stated before it bites rather than silently truncating. */
  const saveQuery = useCallback(
    (sql: string, block?: Block) => {
      const text = String(sql || '').trim();
      if (!text) {
        toast('Write a query first, then keep it.');
        return;
      }
      const c = course;
      /* Only a real exercise gets an exercise id; an example's id is a demo
         label, and treating it as an exercise would put a fake one in the
         workbook's dedupe key. */
      const exercise = block?.type === 'exercise' ? block.id : undefined;
      const saved = progress.saved || [];
      if (saved.some((q) => q.course === c.id && q.exercise === exercise && q.sql === text)) {
        toast('That query is already in your workbook.');
        return;
      }
      if (saved.filter((q) => q.course === c.id).length >= SAVED_PER_COURSE) {
        toast(`Your workbook keeps ${SAVED_PER_COURSE} queries per course. Remove one first.`);
        return;
      }
      const vers = stampFor(block, datasetVersion);
      const next: Progress = {
        ...progress,
        saved: [
          ...saved,
          {
            course: c.id,
            lesson: activeLesson + 1,
            name: (exercise ? eventTitle(exercise) : block?.note ? String(block.note) : text.split('\n')[0]).slice(0, 60),
            sql: text,
            exercise,
            contract: block?.contract,
            datasetVersion: vers.datasetVersion,
            exerciseVersion: vers.exerciseVersion,
            ts: Date.now(),
          },
        ],
      };
      setProgress(next);
      persistProgress(next);
      syncProgress(next, c.id);
      toast('Query kept in your workbook.');
    },
    [course, progress, activeLesson, persistProgress, syncProgress, toast]
  );

  const removeSaved = useCallback(
    (ts: number) => {
      const c = course;
      const next: Progress = {
        ...progress,
        saved: (progress.saved || []).filter((q) => !(q.course === c.id && q.ts === ts)),
      };
      setProgress(next);
      persistProgress(next);
      syncProgress(next, c.id);
      toast('Saved query removed.');
    },
    [course, progress, persistProgress, syncProgress, toast]
  );

  const removeWorkbookItem = useCallback(
    (ts: number) => {
      const c = course;
      const next: Progress = {
        ...progress,
        notes: (progress.notes || []).filter((n) => !(n.course === c.id && n.ts === ts)),
      };
      setProgress(next);
      persistProgress(next);
      syncProgress(next, c.id);
      toast('Removed from your workbook.');
    },
    [course, progress, persistProgress, syncProgress, toast]
  );

  const isBookmarked = useCallback(
    (lesson: number, section: number) =>
      (progress.notes || []).some(
        (n) => n.course === course.id && n.kind === 'bookmark' && n.lesson === lesson && n.section === section,
      ),
    [progress.notes, course.id]
  );

  const openBookmark = useCallback(
    (lesson: number, section?: number) => {
      const mods = activeModulesOf(course);
      openLessonIn(course.id, Math.max(0, lesson - 1));
      if (section == null) return;
      const mod = mods[lesson - 1];
      if (!mod) return;
      /* The lesson screen paints its sections after the state change lands, so
         the jump waits one tick rather than grabbing a node that is not there. */
      setTimeout(() => {
        scrollToNode(document.getElementById(`sec-${mod.id}-${section}`));
      }, 80);
    },
    [course, openLessonIn]
  );

  const openSearchHit = useCallback(
    (hit: { course: string; moduleId: string; lesson: number; section?: number; exercise?: string }) => {
      openLessonIn(hit.course, Math.max(0, hit.lesson - 1));
      const target = hit.exercise
        ? `cell-${hit.exercise}`
        : hit.section != null
          ? `sec-${hit.moduleId}-${hit.section}`
          : '';
      if (!target) return;
      /* The chapter renders after the navigation state lands. */
      setTimeout(() => {
        scrollToNode(document.getElementById(target));
      }, 90);
    },
    [openLessonIn]
  );

  const clearProgress = useCallback(() => {
    const p = emptyProgress();
    setProgress(p);
    persistProgress(p);
    syncProgress(p, uiState.selectedCourseId);
    toast('Your data was cleared on this account.');
  }, [persistProgress, syncProgress, uiState.selectedCourseId, toast]);

  /* Sample data is a demo, not a default. It has to be asked for by name so the
     workspace never looks pre-populated with work nobody did. */
  const loadSampleProgress = useCallback(() => {
    const p = seedProgress();
    setProgress(p);
    persistProgress(p);
    syncProgress(p, uiState.selectedCourseId);
    toast('Sample data loaded. Clear it any time from Appearance.');
  }, [persistProgress, syncProgress, uiState.selectedCourseId, toast]);

  /* The export is built from the same Progress the server holds, plus the
     local editor drafts, so it is complete and credential-free. */
  const exportData = useCallback(() => {
    const filename = downloadExport(buildExportBundle(progress, collectDrafts(), datasetVersion));
    toast(`Exported to ${filename}`);
    return filename;
  }, [progress, toast]);

  /* The other half of the same document. Validation happens before anything is
     written, the merge never removes what is already here, and the adapter is
     whichever one is live — the merge itself is adapter-independent. */
  const importData = useCallback(
    (text: string): ImportOutcome => {
      const parsed = readImport(text);
      if (!parsed.ok) {
        toast(parsed.message);
        return parsed;
      }
      const result = applyImport(progress, parsed.bundle, collectDrafts());
      setProgress(result.progress);
      persistProgress(result.progress);
      syncProgress(result.progress, uiState.selectedCourseId);
      for (const [cellId, sql] of Object.entries(result.drafts)) api.saveDraft(cellId, sql);
      toast(describeImport(result));
      return result;
    },
    [api, progress, persistProgress, syncProgress, uiState.selectedCourseId, toast],
  );

  /* ---------- course content mutations (bump catalogVersion to re-render) ---------- */
  const bumpCatalog = useCallback(() => setCatalogVersion((v) => v + 1), []);

  /* A course the gateway does not have. Both spellings are the same fact: the
     generic route answers 404 `NOT_FOUND`, the AI contract names the same thing
     `AI_NOT_FOUND`. */
  function isMissingCourse(e: unknown): boolean {
    const err = e as { code?: string; status?: number } | null;
    return err?.status === 404 || err?.code === 'NOT_FOUND' || err?.code === 'AI_NOT_FOUND';
  }

  /* ---------- M8: the learner's stored courses ----------
   *
   * The gateway stores a generated course; this tab used to keep it only in
   * React state, so a reload threw it away and `#/course/{id}` landed on
   * whatever built-in course the router fell back to. Everything below makes
   * the gateway the source of truth: read the list on sign-in, resolve a course
   * a link names, and delete through the API so the change is the server's.
   *
   * `gateway: true` is the flag that says where a course came from. A built-in
   * course never carries it, so the two can never be confused for one another,
   * and it is what tells a later list refresh which entries are the learner's
   * own records and may be replaced or dropped. */
  const materialise = useCallback(
    (rec: AiCourseRecord): Course =>
      ({
        ...courseFromPlan(rec.plan, rec.plan.subtitle || rec.plan.title),
        gateway: true,
        recordStatus: rec.status,
        createdAt: rec.created_at,
      }) as Course,
    []
  );

  /* Mirror a list of stored courses into the catalog. The catalog is what every
     screen and the router resolve an id against, so a stored course has to be
     in it to be reachable; a course that is no longer in the list is dropped,
     so a delete is visible everywhere at once. */
  const applyStored = useCallback(
    (next: Course[]) => {
      const keep = new Set(next.map((c) => c.id));
      for (let i = COURSES.length - 1; i >= 0; i--) {
        const c = COURSES[i];
        if (c.gateway === true && !keep.has(c.id)) COURSES.splice(i, 1);
      }
      for (const c of next) {
        const at = COURSES.findIndex((x) => x.id === c.id);
        if (at >= 0) COURSES[at] = c;
        else COURSES.push(c);
      }
      storedRef.current = next;
      setStoredCourses(next);
      bumpCatalog();
    },
    [bumpCatalog]
  );

  /* The learner's courses, read once the session is known. `session` is a
     dependency because the list belongs to the account: signing in as somebody
     else must not keep the previous account's courses on screen. */
  useEffect(() => {
    if (api.mode !== 'http' || !authReady || !session) return;
    let alive = true;
    void api
      .listCourses()
      .then((records) => {
        if (alive) applyStored(records.map(materialise));
      })
      .catch(() => {
        /* The list is unavailable. Nothing is dropped: the catalog keeps what it
           already has rather than presenting a failed read as "no courses". */
      });
    return () => {
      alive = false;
    };
  }, [api, authReady, session, applyStored, materialise]);

  /* A run this tab started before the page went away. The gateway went on
     building it; only the view of it was lost, and asking the gateway what
     became of it is the whole recovery. A run that is still going hands the
     learner back to the screen that watches it; one that has finished, been
     refused, or gone entirely leaves nothing to watch, and the course it wrote
     is already in the list read just above. */
  useEffect(() => {
    if (api.mode !== 'http' || !authReady || !session) return;
    /* Only from the app's own landing places. A link that names a screen is the
       learner saying where they want to be, and a run that is still going does
       not overrule that: the breadcrumb is kept, and the course it is writing
       keeps growing in the library either way. */
    if (entry.screen !== 'home' && entry.screen !== 'create') return;
    const breadcrumb = readRun(session.user.id);
    if (!breadcrumb) return;
    let alive = true;
    void api
      .generationJob(breadcrumb.jobId)
      .then((job) => {
        if (!alive) return;
        if (job && jobIsRunning(job)) {
          setResumeRun({ jobId: breadcrumb.jobId, goal: breadcrumb.goal });
          setScreen('building');
          return;
        }
        clearRun(breadcrumb.jobId);
      })
      .catch(() => {
        /* The gateway could not be asked. The breadcrumb is kept: the run may
           still be going, and a later load can try again. */
      });
    return () => {
      alive = false;
    };
  }, [api, authReady, session, entry.screen]);

  /* The breadcrumb, written when a run is submitted and cleared once it is
     over. It carries the account so a second learner on this browser is never
     offered somebody else's run, and the goal because a resumed page has no
     form left to read it from. Nothing about the run's state is stored: that
     is read back from the gateway, always. */
  const runStarted = useCallback(
    (jobId: string, goal: string): void => {
      const ownerId = sessionRef.current?.user.id;
      if (!ownerId) return;
      writeRun({ jobId, ownerId, goal, startedAt: Date.now() });
    },
    []
  );

  const runSettled = useCallback((jobId: string): void => {
    clearRun(jobId);
    setResumeRun((prev) => (prev && prev.jobId === jobId ? null : prev));
  }, []);

  /* A course the fragment named. The gateway may own an id this tab has never
     seen, so the store asks before the router is allowed to give up. */
  useEffect(() => {
    if (!pendingCourse || !authReady) return;
    if (!session) return;
    const goHome = (message: string | null) => {
      setPendingCourse(null);
      setScreen('home');
      window.history.replaceState(null, '', HOME_HASH);
      if (message) toast(message);
    };
    if (api.mode !== 'http') {
      /* The offline adapters keep no course store, so this link is not theirs. */
      goHome(null);
      return;
    }
    let alive = true;
    void api
      .getCourse(pendingCourse.id)
      .then((rec) => {
        if (!alive) return;
        if (!rec) {
          goHome('That course is not in your library.');
          return;
        }
        const course = materialise(rec);
        const others = storedRef.current.filter((c) => c.id !== course.id);
        applyStored([...others, course]);
        if (pendingCourse.chapterId) {
          const index = (course.modules || []).findIndex((m) => m.id === pendingCourse.chapterId);
          if (index >= 0) setActiveLesson(index);
        }
        setPendingCourse(null);
      })
      .catch(() => {
        if (alive) goHome('That course could not be opened.');
      });
    return () => {
      alive = false;
    };
  }, [api, authReady, session, pendingCourse, applyStored, materialise, toast]);

  /* Removing a course is the gateway's decision, so it goes there first and the
     list changes only once it agreed. A course the gateway has already
     forgotten is not a failure: the learner asked for it to be gone and it is. */
  const deleteCourse = useCallback(
    async (id: string): Promise<boolean> => {
      if (api.mode !== 'http') {
        toast('Generated courses live on the gateway; there is nothing to remove offline.');
        return false;
      }
      try {
        await api.deleteCourse(id);
      } catch (e) {
        /* A course the gateway has already forgotten is the outcome the learner
           asked for, so it is not a failure: the route answers 404 `NOT_FOUND`
           (and the AI surface also spells it `AI_NOT_FOUND`). Anything else is a
           real refusal and the list is left exactly as it was. */
        if (!isMissingCourse(e)) {
          toast('The course could not be removed. Try again.');
          return false;
        }
      }
      applyStored(storedRef.current.filter((c) => c.id !== id));
      if (uiStateRef.current.selectedCourseId === id) {
        const next = { ...uiStateRef.current, selectedCourseId: COURSES[0].id };
        uiStateRef.current = next;
        saveUiState(next);
        setActiveLesson(0);
        setScreen('library');
      }
      toast('Course removed.');
      return true;
    },
    [api, applyStored, saveUiState, toast]
  );

  /* Register one stored course from the gateway, replacing any copy already in
     the catalog. Used mid-run: a course the pipeline is still writing is real,
     it is the learner's, and reading it as it grows is the whole point of
     generating chapter by chapter. Returns the catalog course so the caller can
     show it without waiting for a render. */
  const adoptStoredCourse = useCallback(
    (rec: AiCourseRecord): Course => {
      const course = materialise(rec);
      applyStored([...storedRef.current.filter((c) => c.id !== course.id), course]);
      return course;
    },
    [applyStored, materialise]
  );

  /* Course authoring is the admin surface. The screens and nav hide it from a
     learner, but these actions mutate the shared draft, so they enforce the rule
     themselves rather than trusting that no other path reached them. */
  const canAuthor = useCallback(() => {
    if (session?.user.role === 'admin') return true;
    toast('Course authoring needs an admin account.');
    return false;
  }, [session, toast]);

  const moveLesson = useCallback(
    (i: number, dir: number) => {
      if (!canAuthor()) return;
      const j = i + dir;
      if (j < 0 || j >= MODULES.length) return;
      const t = MODULES[i];
      MODULES[i] = MODULES[j];
      MODULES[j] = t;
      MODULES.forEach((m, k) => (m.n = k + 1));
      setActiveLesson(j);
      bumpCatalog();
      toast('Chapter index updated.');
    },
    [bumpCatalog, canAuthor, toast]
  );

  const saveCourseEdits = useCallback(
    ({ title }: { title?: string; goal?: string; context?: string; dialect?: string }) => {
      if (!canAuthor()) return;
      if (title && title.trim()) COURSES[0].title = title.trim();
      bumpCatalog();
      toast('Course details saved.');
    },
    [canAuthor, bumpCatalog, toast]
  );

  const saveLessonEdits = useCallback(
    ({ title, summary }: { title?: string; summary?: string }) => {
      if (!canAuthor()) return;
      const m = MODULES[activeLesson];
      if (!m) return;
      if (title && title.trim()) m.title = title.trim();
      if (summary && summary.trim()) m.summary = summary.trim();
      bumpCatalog();
      toast('Chapter updated.');
    },
    [canAuthor, activeLesson, bumpCatalog, toast]
  );

  const applyExtension = useCallback(
    (kind: string, prompt?: string) => {
      if (!canAuthor()) return;
      const PRESETS: Record<string, string[]> = {
        deeper: ['Edge cases and advanced variants', 'Add harder exercises with ties, NULLs, missing periods, duplicate-producing joins, and performance trade-offs.'],
        advanced: ['Advanced SQL patterns', 'Add advanced modules on lateral joins, recursive CTEs, percentile analysis, and efficient analytical queries.'],
        project: ['Integrated capstone project', 'Add an open-ended marketplace analytics project with staged milestones, tests, and a final review.'],
        branch: ['Query planning and indexing branch', 'Add an optional branch on EXPLAIN ANALYZE, index design, cardinality estimates, and query rewrites.'],
        practice: ['Interview practice set', 'Add a graduated set of interview problems with hidden edge cases and explanation after submission.'],
        context: ['Domain-specific adaptation', 'Adapt exercises to a domain-specific schema and business rules supplied by the learner.'],
      };
      const p = PRESETS[kind] || PRESETS.deeper;
      const ts = Date.now();
      MODULES.push({
        id: 'ext' + ts,
        n: MODULES.length + 1,
        title: p[0],
        kind: 'Added',
        minutes: 20,
        summary: (prompt && prompt.trim()) || p[1],
        objectives: ['Apply earlier patterns to a new scenario'],
        blocks: [
          { type: 'prose', html: (prompt && prompt.trim()) || p[1] },
          {
            type: 'exercise',
            id: 'ex.ext.' + ts,
            prompt: 'Write a query that demonstrates: ' + ((prompt && prompt.trim()) || p[0]),
            starter: 'SELECT\n    -- your query\nFROM orders;',
            solution: "SELECT id FROM orders WHERE status = 'completed' LIMIT 10;",
            hints: ['Reuse the patterns from earlier chapters.'],
            contract: { columns: ['id'], column_order: 'exact', row_order: 'ignored' },
            checks: [{ label: 'Selects from orders', code: 'JOIN_MISSING', test: (s) => /from\s+orders/i.test(s) }],
            result: { columns: ['id'], rows: [['1001'], ['1002'], ['1004'], ['1005'], ['1006']] },
          },
        ],
      });
      bumpCatalog();
      openLessonIn('sql-analytics', MODULES.length - 1);
      toast('Extension added to the draft.');
    },
    [canAuthor, bumpCatalog, openLessonIn, toast]
  );

  const addLesson = useCallback(
    ({ title, goal }: { title: string; goal?: string; position?: string }) => {
      if (!canAuthor()) return;
      if (!title || !title.trim()) return;
      const desc = (goal && goal.trim()) || 'New chapter generated from your request';
      const ts = Date.now();
      MODULES.push({
        id: 'new' + ts,
        n: MODULES.length + 1,
        title: title.trim(),
        kind: 'Added',
        minutes: 15,
        summary: desc,
        objectives: ['Meet the goal you described'],
        blocks: [
          { type: 'prose', html: desc },
          {
            type: 'exercise',
            id: 'ex.new.' + ts,
            prompt: 'Write a query for: ' + title.trim(),
            starter: 'SELECT\n    -- your query\nFROM orders;',
            solution: "SELECT id FROM orders WHERE status = 'completed' LIMIT 10;",
            hints: ['Start from the tables you already know.'],
            contract: { columns: ['id'], column_order: 'exact', row_order: 'ignored' },
            checks: [{ label: 'Selects from orders', code: 'JOIN_MISSING', test: (s) => /from\s+orders/i.test(s) }],
            result: { columns: ['id'], rows: [['1001'], ['1002'], ['1004']] },
          },
        ],
      });
      bumpCatalog();
      toast('Chapter added to the draft.');
    },
    [canAuthor, bumpCatalog, toast]
  );

  const addSection = useCallback(
    (title: string) => {
      if (!canAuthor()) return;
      const m = activeModulesOf(course)[activeLesson];
      if (!m || !title) return;
      const ts = Date.now();
      m.blocks.push({ type: 'prose', section: title, html: `<em>${title}</em> — added by you. Write the explanation here, then attach the exercise below.` });
      m.blocks.push({
        type: 'exercise',
        id: 'ex.' + m.id + '.' + ts,
        prompt: 'Practice: ' + title,
        starter: 'SELECT\n    -- your query\nFROM orders;',
        solution: "SELECT id FROM orders WHERE status = 'completed' LIMIT 10;",
        hints: ['Reuse the patterns from earlier sections.'],
        contract: { columns: ['id'], column_order: 'exact', row_order: 'ignored' },
        checks: [{ label: 'Selects from orders', code: 'JOIN_MISSING', test: (s) => /from\s+orders/i.test(s) }],
        result: { columns: ['id'], rows: [['1001'], ['1002'], ['1004'], ['1005'], ['1006']] },
      });
      bumpCatalog();
      toast(`Section added to chapter ${m.n}.`);
    },
    [canAuthor, course, activeLesson, bumpCatalog, toast]
  );

  const submitBuild = useCallback(
    (goal: string, context?: { level?: string; source?: string; requireApproval?: boolean }) => {
      if (!canAuthor()) return;
      const g = (goal || '').trim();
      if (g) setGoalInput(g);
      setBuildContext({
        level: (context?.level || '').trim() || undefined,
        source: (context?.source || '').trim() || undefined,
        /* The outline pause is opt-in: a learner who does not ask for it gets
           today's one-request run, outline straight into chapters. */
        requireApproval: context?.requireApproval === true ? true : undefined,
      });
      /* This only records the ask. The draft itself is produced by the build
         that runs next: a generated course when a provider is configured, and
         the built-in curriculum — labelled as such — when one is not. Nothing
         here assembles a course, because a screen must never show a course the
         pipeline has not produced. */
      setDraftCourseId(null);
      setDraftReport(null);
      setReviewGoal(g || 'Learn to answer realistic product and business questions with SQL, then explain the reasoning behind your query.');
      if (context?.level || context?.source) setContextState('User context added');
      else setContextState('AI-generated');
      setCourseSubtitle('A draft shaped around your goal. Explore the chapters, sample chapter, data model, and validation notes before starting.');
      bumpCatalog();
    },
    [canAuthor, bumpCatalog]
  );

  /* The built-in course as a draft, used only when no provider is configured.
     It is the authored curriculum with course-scoped ids, and the report it
     produces says `authored` so no screen can present it as generated. */
  const useAuthoredDraft = useCallback(
    (goal: string) => {
      const g = (goal || '').trim();
      const advanced = /advanced|window|analytics|cohort|query plan|performance|retention/i.test(g);
      const id = `created-${Date.now()}`;
      const modules = draftModulesFor(`@${id}`);
      const draft: Course = {
        id,
        title: advanced ? 'SQL Analytics in the Real World' : 'SQL Joins and Interview Practice',
        subtitle: 'The built-in curriculum, opened as a draft. No AI was involved: these chapters ship with the app.',
        source: 'created',
        enrolled: true,
        built: true,
        dialect: 'postgresql',
        modules,
        blurb: blurbFromGoal(g),
        domain: 'Commerce & retail',
        level: advanced ? 'Intermediate' : 'Beginner → intermediate',
        minutes: modules.reduce((total, m) => total + (m.minutes || 0), 0),
        authored: true,
      };
      COURSES.push(draft);
      setDraftCourseId(id);
      setCourseSubtitle(String(draft.subtitle));
      bumpCatalog();
      return id;
    },
    [bumpCatalog]
  );

  /* Adopt a course the gateway generated and verified. The plan is only ever
     present on an active outcome, so reaching the branch that builds a Course
     means every exercise in it already ran and cross-checked on the engine. A
     rejected run records the report and returns null: the screens show the
     failure, and no course is offered. */
  const adoptGeneratedCourse = useCallback(
    (outcome: AiOutcome, goal: string) => {
      setDraftReport(reportFromOutcome(outcome, goal));
      if (outcome.status !== 'active' || !outcome.plan) return null;
      /* `gateway: true` records where this course came from, so a later list
         refresh knows it is the learner's own record and may replace it with
         the stored copy rather than leaving two versions of one course. */
      const course = { ...courseFromPlan(outcome.plan, goal), gateway: true, recordStatus: 'active' } as Course;
      // Idempotent: a retry of the same plan must not add a second copy.
      const existing = COURSES.findIndex((c) => c.id === course.id);
      if (existing >= 0) COURSES[existing] = course;
      else COURSES.push(course);
      setDraftCourseId(course.id);
      setCourseSubtitle(String(course.subtitle ?? ''));
      bumpCatalog();
      return { id: course.id, title: course.title };
    },
    [bumpCatalog]
  );

  const value = {
    // data
    COURSES,
    MODULES,
    progress,
    uiState,
    course,
    draftCourse,
    modules,
    pct,
    activeLesson,
    // api
    api,
    apiLog,
    apiCount,
    engineCaps,
    aiAvailability,
    adminPasswordEmpty,
    latency,
    setLatency,
    setLibraryFilter: (f: string) => {
      const next = { ...uiState, libraryFilter: f };
      setUiState(next);
      saveJSON(STATE_KEY, next);
    },
    // accounts
    session,
    authReady,
    authNotice,
    role,
    signup,
    login,
    logout,
    // ui
    screen,
    showScreen,
    challengeId,
    openChallenge,
    reviewTab,
    setReviewTab,
    tablesOpen,
    setTablesOpen,
    chaptersOpen,
    setChaptersOpen,
    navOpen,
    setNavOpen,
    modal,
    openModal: setModal,
    closeModal: () => setModal(null),
    toastMsg,
    toast,
    promptRequest,
    requestPrompt: setPromptRequest,
    closePrompt: () => setPromptRequest(null),
    // theme
    theme,
    effectiveTheme,
    setTheme: (m: ThemeName) => {
      setThemeState(m);
      saveJSON(THEME_KEY, m);
    },
    cycleTheme: () => {
      const next = theme === 'light' ? 'dark' : 'light';
      setThemeState(next);
      saveJSON(THEME_KEY, next);
    },
    motion,
    setMotion: () => {
      const next = !motion;
      setMotionState(next);
      saveJSON(MOTION_KEY, next);
    },
    // navigation
    openLessonIn,
    openLesson,
    selectCourse,
    enrollCourse,
    resumeCourse,
    startLearning,
    stepLesson,
    markLessonDone,
    courseShort,
    // progress
    saveProgress,
    recordEvent,
    addNote,
    toggleBookmark,
    removeWorkbookItem,
    openBookmark,
    isBookmarked,
    openSearchHit,
    saveQuery,
    removeSaved,
    addStudyTime,
    loadSampleProgress,
    clearProgress,
    exportData,
    importData,
    // content mutations
    catalogVersion,
    content,
    moveLesson,
    saveCourseEdits,
    saveLessonEdits,
    applyExtension,
    addLesson,
    addSection,
    // create/build/review draft
    goalInput,
    setGoalInput,
    reviewGoal,
    setReviewGoal,
    courseSubtitle,
    setCourseSubtitle,
    contextState,
    setContextState,
    submitBuild,
    useAuthoredDraft,
    adoptGeneratedCourse,
    draftReport,
    setDraftReport,
    buildContext,
    // M8 stored courses
    storedCourses,
    courseResolving,
    deleteCourse,
    adoptStoredCourse,
    resumeRun,
    runStarted,
    runSettled,
    // misc
    moduleExerciseBlocks: moduleExerciseBlocks as (m: CourseModule) => Block[],
  };

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}

export function useStore() {
  const ctx = useContext(StoreContext);
  if (!ctx) throw new Error('useStore must be used within StoreProvider');
  return ctx;
}
