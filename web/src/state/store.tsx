import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createApi, readAuthToken } from '../api/index';
import { COURSES, MODULES } from '../data/curriculum';
import {
  activeModules as activeModulesOf,
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
import { datasetVersion } from '../api/dataset';
import {
  PROGRESS_VERSION,
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
const PROG_KEY = 'qc.progress';
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
  } catch (e) {
    return fallback;
  }
}
function saveJSON(k: string, v: unknown): void {
  try {
    localStorage.setItem(k, JSON.stringify(v));
  } catch (e) {}
}

/** Everything screens receive from useStore(). */
export interface StoreValue {
  COURSES: Course[];
  MODULES: CourseModule[];
  progress: Progress;
  uiState: UiState;
  course: Course;
  modules: CourseModule[];
  pct: number;
  activeLesson: number;

  api: ExecutionApi;
  apiLog: ApiLogEntry[];
  apiCount: number;
  /** Where SQL runs and whether a runaway query can be stopped (design §5.2). */
  engineCaps: EngineCapabilities | null;
  latency: boolean;
  setLatency: React.Dispatch<React.SetStateAction<boolean>>;
  setLibraryFilter: (f: string) => void;

  /* Accounts. `session` is null when signed out (the app then shows the auth
     screen in gateway/mock mode); `role` gates the authoring surfaces. */
  session: AuthSession | null;
  authReady: boolean;
  authNotice: string | null;
  role: UserRole;
  signup: (input: SignupInput) => Promise<void>;
  login: (input: LoginInput) => Promise<void>;
  logout: () => Promise<void>;

  screen: Screen;
  showScreen: (id: Screen) => void;
  reviewTab: string;
  setReviewTab: React.Dispatch<React.SetStateAction<string>>;
  tablesOpen: boolean;
  setTablesOpen: React.Dispatch<React.SetStateAction<boolean>>;
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
  submitBuild: (goal: string, hasContext?: boolean) => string | undefined;

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
function initialProgress(): Progress {
  const raw = loadJSON<unknown>(PROG_KEY, null);
  if (raw && !isSampleProgress(raw)) {
    const migrated = migrateProgress(raw);
    if (migrated) return migrated;
  }
  /* Nothing usable on disk: a missing blob, generated sample data, or a
     snapshot too old to migrate. Start empty and overwrite the stale row so
     every boot does not have to decide to ignore it again. */
  const fresh = emptyProgress();
  saveJSON(PROG_KEY, fresh);
  return fresh;
}

export function StoreProvider({ children }: { children: React.ReactNode }) {
  const [uiState, setUiState] = useState<UiState>(() => {
    const s = loadJSON<UiState | null>(STATE_KEY, null) || { selectedCourseId: 'sql-analytics', libraryFilter: 'all' };
    if (!s.libraryFilter) s.libraryFilter = 'all';
    return s;
  });
  const [progress, setProgress] = useState(initialProgress);
  const [screen, setScreen] = useState<Screen>('home');
  const [activeLesson, setActiveLesson] = useState(() => nextChapterIndexOf(COURSES[0], initialProgress()));
  const [theme, setThemeState] = useState<ThemeName>(() => loadJSON<ThemeName>(THEME_KEY, 'light'));
  const [motion, setMotionState] = useState<boolean>(() => loadJSON<boolean>(MOTION_KEY, false));
  const [reviewTab, setReviewTab] = useState('overview');
  const [tablesOpen, setTablesOpen] = useState(false);
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
  const [session, setSession] = useState<AuthSession | null>(null);
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
   * gateway (and the mock adapter, which fakes the same surface locally) reports
   * the current account from the stored token; no account means the auth screen.
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

  /* Chapter content lives in the app database; load it once when the API is up.
     The module list is derived from the courses rather than named here: a
     hand-written list silently omitted a whole course the moment one was added,
     and the lessons then fell back to the bundled prose with no error to notice. */
  useEffect(() => {
    let alive = true;
    const ids = COURSES.flatMap((c) => c.modules ?? []).map((m) => m.id);
    Promise.all(ids.map(async (id) => [id, await api.getContent(id)] as const))
      .then((entries) => {
        if (alive) setContent(Object.fromEntries(entries.filter(([, docs]) => docs.length > 0)));
      })
      .catch(() => {
        /* the lesson page falls back to the bundled prose blocks */
      });
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

  const persistProgress = useCallback((next?: Progress) => {
    const p = next || progressRef.current;
    dirtyRef.current = true;
    saveJSON(PROG_KEY, p);
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
  }, [api]);

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
        code === 'PERSISTENCE_UNAVAILABLE'
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

  /* Learner data lives on the server, so pull it once on boot.
   *
   * Without this the UI was localStorage-only: the gateway recorded progress
   * that no client ever read back, so a second browser (or a cleared cache)
   * showed an empty workspace next to a full server row — the classic
   * "it's a mock" tell. The server snapshot now wins, but only if the learner
   * has not already touched something locally while the request was in flight. */
  useEffect(() => {
    /* Progress and events are owner-scoped by the bearer token, so there is
       nothing to pull until the session is restored and a user is present. */
    if (!authReady || !session) return;
    if (hydratedRef.current) return;
    hydratedRef.current = true;
    let alive = true;
    api
      .getProgress(uiState.selectedCourseId)
      .then(({ ok, snapshot }) => {
        if (!alive || !ok || !snapshot || typeof snapshot !== 'object') return;
        const remote = snapshot as Progress;
        /* A server row that only holds generated sample data is legacy residue
           from the old first-run seed; drop it instead of showing it as real. */
        if (isSampleProgress(remote)) {
          syncProgress(emptyProgress(), uiState.selectedCourseId);
          return;
        }
        const migrated = migrateProgress(remote);
        if (!migrated || dirtyRef.current) return;
        const remoteProgress = migrated;
        setProgress(remoteProgress);
        saveJSON(PROG_KEY, remoteProgress);
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
  const modules = activeModulesOf(course);
  const pct = lessonPercentOf(course, progress);

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
    const c = COURSES[0];
    c.enrolled = true;
    selectCourse(c.id);
    showScreen('course');
    toast('Added to My learning.');
  }, [selectCourse, showScreen, toast]);

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
    const next = { ...progress, done: { ...progress.done, [m.id]: !progress.done[m.id] } };
    setProgress(next);
    persistProgress(next);
    /* Chapter completion is progress like any other mutation, so it must reach
       the gateway too — otherwise the server snapshot disagrees with the UI and
       the signal is lost on a different browser or a cleared cache. */
    syncProgress(next, uiState.selectedCourseId);
    toast(next.done[m.id] ? 'Chapter marked complete.' : 'Chapter marked incomplete.');
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
        const el = document.getElementById(`sec-${mod.id}-${section}`);
        if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
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
        const el = document.getElementById(target);
        if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
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
    (goal: string, hasContext?: boolean) => {
      if (!canAuthor()) return;
      const g = (goal || '').trim();
      const advanced = /advanced|window|analytics|cohort|query plan|performance|retention/i.test(g);
      const title = advanced ? 'SQL Analytics in the Real World' : 'SQL Joins and Interview Practice';
      COURSES[0].title = title;
      setReviewGoal(g || 'Learn to answer realistic product and business questions with SQL, then explain the reasoning behind your query.');
      setCourseSubtitle('A draft shaped around your goal. Explore the chapters, sample chapter, data model, and validation notes before starting.');
      if (hasContext) setContextState('User context added');
      if (g) setGoalInput(g);
      bumpCatalog();
      return title;
    },
    [canAuthor, bumpCatalog]
  );

  const value = {
    // data
    COURSES,
    MODULES,
    progress,
    uiState,
    course,
    modules,
    pct,
    activeLesson,
    // api
    api,
    apiLog,
    apiCount,
    engineCaps,
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
    role: session?.user.role ?? 'learner',
    signup,
    login,
    logout,
    // ui
    screen,
    showScreen,
    reviewTab,
    setReviewTab,
    tablesOpen,
    setTablesOpen,
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
