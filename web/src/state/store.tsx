/*
 * The single app provider. It is the largest file in web/src, and a review
 * flagged that: ~90 members spanning auth, the hash router, progress
 * persistence + IndexedDB sync, the AI build orchestration, the catalog and
 * theme. That is a real maintainability note, not a bug. The tracked split is
 * done: the router (`useHashRouter.ts`), progress (`useProgressStore.ts`),
 * catalog (`useCatalog.ts`), theme + motion (`useTheme.ts`) and auth + session
 * (`useAuth.ts`) each live in their own module. The AI build orchestration
 * already lives in `components/screens/useGenerationRun.ts`; what remains here
 * is the provider state those hooks share. This banner exists so the next
 * reader sees the map instead of rediscovering it.
 *
 * Concerns, in this order:
 *   1. auth + session (moved to useAuth.ts: the stored-session restore, the
 *      session, `authReady`, the expired-session notice, `role`, and
 *      signup/login/logout/onUnauthorized)
 *   2. the hash router (moved to useHashRouter.ts: applyHash/showScreen)
 *   3. progress (moved to useProgressStore.ts: load, the events, notes,
 *      bookmarks, saved queries, workbook, study time, chapter completion,
 *      export/import mutations, owner-scoped persist, gateway sync, IndexedDB)
 *   4. the AI build orchestration (draft → outline → chapters → activate)
 *   5. catalog (moved to useCatalog.ts: the stored-course list read and mirror,
 *      the fragment resolve, deletion and mid-run adoption, and the
 *      `catalogVersion` re-render trigger the authoring mutations call)
 *   6. theme + motion (moved to useTheme.ts: the device theme and motion
 *      preferences, the user-defined theme list, the account sync that carries
 *      them across devices, and the effects that set data-theme/data-motion)
 */
import React, { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createApi } from '../api/index';
import { readAuthUser } from '../api/execution/shared';
import { COURSES, MODULES } from '../data/curriculum';
import {
  activeModules as activeModulesOf,
  courseById,
  currentCourse as currentCourseOf,
  lessonPercent as lessonPercentOf,
  nextChapterIndex as nextChapterIndexOf,
} from '../lib/model';
import type { RouteTarget } from '../lib/route';
import { initialRoute, useHashRouter } from './useHashRouter';
import type { PendingCourse } from './useHashRouter';
import { initialProgress, LOCAL_PROGRESS_ID, useProgressStore } from './useProgressStore';
import { useCatalog } from './useCatalog';
import { useTheme } from './useTheme';
import { useAuth } from './useAuth';
import type { AuthStore } from './useAuth';
import { loadJSON, saveJSON } from './jsonStore';
import { datasetFor } from '../lib/dataset';
import { dialectFor } from '../lib/dialect';
import { warmUpCourse } from '../lib/courseWarmup';
import { scrollToNode } from '../lib/scroll';
import type { UserTheme } from '../lib/userThemes';
import { clearRun, readRun, writeRun } from '../lib/runResume';
import { jobIsRunning } from '../api/ai';
import type { AiAvailability, AiOutcome } from '../api/aiContract';
import type { AiChapterEdit, AiCourseRecord } from '../api/aiContract';
import { courseFromPlan, reportFromOutcome, type DraftReport } from '../lib/draft';
import type { ImportOutcome } from '../lib/importBundle';
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
  Screen,
  SignupInput,
  ThemePreference,
  TutorTarget,
  UiState,
  UserRole,
} from '../types';

const STATE_KEY = 'qc.state';

/** A short course blurb the draft owns, derived from the learner's own goal.
    It is never the built-in course's blurb. */
function blurbFromGoal(goal: string): string {
  const g = goal.trim().replace(/\s+/g, ' ');
  if (!g) return 'A course shaped around the skill you want to build, with exercises that run on real PostgreSQL.';
  const short = g.length > 150 ? g.slice(0, 147).trimEnd() + '…' : g;
  return `Built from your goal: ${short}`;
}

/** The gateway's own sentence for a refused edit, so the learner reads what the
    server said rather than a generic failure. */
// oxlint-disable-next-line anti-slop/no-unknown-parameters -- a rejected gateway call is untrusted; this reads its message.
function editFailureMessage(e: unknown): string {
  return e instanceof Error && e.message ? e.message : 'The edit could not be saved.';
}

/** Clone the reference curriculum into a draft course's own modules, giving
    every module and exercise a course-scoped id so progress and attempts never
    leak between the draft and the built-in course. */
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
  reviewTab: string;
  setReviewTab: React.Dispatch<React.SetStateAction<string>>;
  tablesOpen: boolean;
  setTablesOpen: React.Dispatch<React.SetStateAction<boolean>>;
  /** The table the Tables drawer should bring into view on this open, or null
      for a plain open. Set by the tutor drawer's table chips. */
  tablesFocus: string | null;
  /** Open the Tables drawer, optionally scrolled to one table. Closing the
      tutor is part of the open: the two share one right-edge slot. */
  openTables: (table?: string) => void;
  /** The Ask tutor drawer and the exercise it describes. Non-null is open. The
      two right-side drawers share one slot, so only one is ever set. */
  tutorTarget: TutorTarget | null;
  openTutor: (target: TutorTarget) => void;
  closeTutor: () => void;
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

  theme: ThemePreference;
  customThemes: UserTheme[];
  saveCustomTheme: (t: UserTheme) => void;
  deleteCustomTheme: (id: string) => void;
  setTheme: (m: ThemePreference) => void;
  motion: boolean;
  setMotion: () => void;

  openLessonIn: (courseId: string, index?: number) => void;
  selectCourse: (id: string) => void;
  enrollCourse: (id: string) => void;
  resumeCourse: () => void;
  startLearning: () => void;
  stepLesson: (d: number) => void;
  markLessonDone: () => void;

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

  /** Chapter reading content, keyed by module id, read from the database. */
  content: Record<string, ContentSection[]>;
  moveLesson: (i: number, dir: number) => void;
  saveCourseEdits: (edits: { title?: string }) => Promise<void>;
  saveLessonEdits: (edits: { title?: string; summary?: string }) => Promise<void>;
  applyExtension: (kind: string, prompt?: string) => void;
  addLesson: (args: { title: string; goal?: string }) => void;
  addSection: (title: string) => void;

  goalInput: string;
  reviewGoal: string;
  setReviewGoal: React.Dispatch<React.SetStateAction<string>>;
  courseSubtitle: string;
  contextState: string;
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

  /* A stored course the library asked to resume: a build the gateway stopped
     writing (`interrupted`) or one still waiting for approval. The screen that
     watches a run continues it, so this is just the handoff. `null` when the
     learner is not resuming anything. */
  resumeBuild: { courseId: string; goal: string } | null;
  /** Hand a stored course to the build screen, which continues it. */
  resumeStoredCourse: (courseId: string, goal: string) => void;
  /** The resume is done or gone; the handoff is cleared. */
  buildSettled: () => void;

}

const StoreContext = createContext<StoreValue | null>(null);

export function StoreProvider({ children }: { children: React.ReactNode }) {
  /* The fragment is the entry point. A deep link, a reload or a paste into the
     address bar has to land on the screen it names, so the first render reads
     the URL instead of always starting at Home and correcting afterwards. Every
     navigation after that goes through `hashchange`. */
  const [entry] = useState<RouteTarget>(initialRoute);
  const [uiState, setUiState] = useState<UiState>(() => {
    const s = loadJSON<UiState | null>(STATE_KEY, null) || { selectedCourseId: 'sql-analytics', libraryFilter: 'all' };
    if (!s.libraryFilter) s.libraryFilter = 'all';
    /* A chapter link names its course as well as its chapter, and opening a
       chapter from inside the app selects that course; a link resolves the
       same way rather than inventing an enrolment. */
    if (entry.courseId) s.selectedCourseId = entry.courseId;
    return s;
  });
  /* The router reads the selection through a ref, so a `hashchange` sees the
     current course without becoming a dependency of the listener. Other
     concerns (course deletion) read and write the same ref. */
  const uiStateRef = useRef(uiState);
  /* initialProgress reads AND writes (it seeds the stored snapshot), so it is
     evaluated once here rather than in two separate state initializers. */
  const [progress, setProgress] = useState<Progress>(() => initialProgress(readAuthUser()));
  const [screen, setScreen] = useState<Screen>(entry.screen);
  const [challengeId, setChallengeId] = useState<string | null>(entry.challengeId);
  const [activeLesson, setActiveLesson] = useState(() => {
    if (entry.chapterIndex != null) return entry.chapterIndex;
    return nextChapterIndexOf(entry.courseId ? courseById(entry.courseId) : COURSES[0], progress);
  });
  const [reviewTab, setReviewTab] = useState('overview');
  const [tablesOpen, setTablesOpen] = useState(false);
  /* A table the drawer should scroll to when it opens. Cleared on close so a
     later plain open (the Tables button, the Escape key) does not jump. */
  const [tablesFocus, setTablesFocus] = useState<string | null>(null);
  const [tutorTarget, setTutorTarget] = useState<TutorTarget | null>(null);
  /* One right-side drawer at a time. The tutor and tables panels describe the
     same workspace from two sides; stacking them would put a second panel
     behind the scrim where the learner cannot reach it. Opening either closes
     the other — this effect is the tables half, `openTutor` is the tutor half. */
  useEffect(() => {
    if (tablesOpen) setTutorTarget(null);
    else setTablesFocus(null);
  }, [tablesOpen]);
  const openTutor = useCallback((target: TutorTarget) => {
    setTablesOpen(false);
    setTutorTarget(target);
  }, []);
  const closeTutor = useCallback(() => setTutorTarget(null), []);
  const openTables = useCallback((table?: string) => {
    setTutorTarget(null);
    setTablesFocus(table ?? null);
    setTablesOpen(true);
  }, []);
  const [chaptersOpen, setChaptersOpen] = useState(false);
  const [navOpen, setNavOpen] = useState(false);
  const [modal, setModal] = useState<ModalName>(null);
  const [toastMsg, setToastMsg] = useState<string | null>(null);
  const [promptRequest, setPromptRequest] = useState<PromptRequest | null>(null);
  const [apiLog, setApiLog] = useState<ApiLogEntry[]>([]);
  const [apiCount, setApiCount] = useState(0);
  const [latency, setLatency] = useState(true);
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
  /* A background run this tab started and had not finished watching when the
     page went away. Read back on boot: the run itself never stopped, only the
     view of it did. */
  const [resumeRun, setResumeRun] = useState<{ jobId: string; goal: string } | null>(null);
  /* A stored course the library asked to resume. The gateway owns the row, so
     this is only which course the build screen should continue. */
  const [resumeBuild, setResumeBuild] = useState<{ courseId: string; goal: string } | null>(null);
  /* A course id a fragment named that the catalog does not have. It may be a
     stored course the gateway owns, so it is fetched before the screen paints:
     a `#/course/{id}` link used to fall back to the first built-in course and
     rewrite the URL, which is how a generated course "vanished" on reload. */
  const [pendingCourse, setPendingCourse] = useState<PendingCourse | null>(() =>
    entry.courseUnresolved && entry.courseId ? { id: entry.courseId, chapterId: entry.chapterId ?? null } : null
  );
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

  /* ---------- the auth concern ---------- */
  /* The stored-session restore, the session, `authReady`, the notice and the
     account actions live in `useAuth`. The adapter is created first, because
     every other concern reads it, and its 401 reaction is auth's; the hook
     hands that reaction back through a stable delegate, so the adapter's
     identity never changes and the hook can rebuild the handler freely. The two
     pieces of wiring the actions trigger stay here: `goHome` moves the router,
     and `resetHydration` clears the progress guards for the new owner. */
  const authRef = useRef<AuthStore | null>(null);
  const api = useMemo(
    () => createApi({ onLog, onUnauthorized: () => authRef.current?.onUnauthorized() }),
    [onLog]
  );
  useEffect(() => {
    if (api.config) api.config.latency = latency;
  }, [api, latency]);

  const goHome = useCallback(() => setScreen('home'), []);
  const resetHydration = useCallback(() => {
    hydratedRef.current = false;
    dirtyRef.current = false;
  }, []);
  const auth = useAuth({ api, goHome, resetHydration });
  /* The api's `onUnauthorized` and the rest of the callbacks read the auth store
     through this ref. A layout effect writes it after commit, before any request
     can call back, so the ref never moves during a render React might discard. */
  useLayoutEffect(() => {
    authRef.current = auth;
  }, [auth]);
  const { session, sessionRef, authReady, authNotice, role, signup, login, logout } = auth;

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

  /* The progress mutations read the snapshot through this ref so they stay
     identity-stable; `useProgressStore` keeps it in sync and reads it. */
  const progressRef = useRef(progress);
  /* Progress is owner-scoped. The owner changes on signup, login, logout, a
     401, and session restore, and the in-memory snapshot has to change with it.
     `useProgressStore` owns that swap; the provider keeps the owner so sign-in
     and the router can read the same value. */
  const ownerId = session?.user.id ?? LOCAL_PROGRESS_ID;
  const progressOwnerRef = useRef<string>((readAuthUser() || LOCAL_PROGRESS_ID));

  /* Where SQL actually runs, as reported by the adapter itself (§5.2). Read
     once the engine has booted; until then the screen keeps its default copy
     rather than guessing. */
  const [engineCaps, setEngineCaps] = useState<EngineCapabilities | null>(null);
  useEffect(() => {
    let alive = true;
    let tries = 0;
    let retry: ReturnType<typeof setTimeout> | null = null;
    const read = () => {
      api
        .capabilities()
        .then((caps) => {
          if (!alive || !caps) {
            if (alive && tries++ < 10) retry = setTimeout(read, 700);
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
      if (retry !== null) clearTimeout(retry);
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

  const course = useMemo(() => currentCourseOf(uiState), [uiState]);
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
  /* M9. A stored course carries its own workspace setup. Preparing it when the
     course opens applies that setup before the first exercise runs, so the
     exercise does not die with "relation does not exist". The id is a
     primitive, so the effect runs once per course change instead of on every
     render, and a prepare refusal is a non-blocking toast, never a navigation
     block. */
  const storedCourseId = course?.gateway === true ? course.id : null;
  useEffect(() => {
    warmUpCourse(api, courseDataset, courseDialect, storedCourseId, toast);
  }, [api, courseDataset, courseDialect, storedCourseId, toast]);
  const draftCourse = draftCourseId ? COURSES.find((c) => c.id === draftCourseId) ?? null : null;
  const modules = useMemo(() => activeModulesOf(course), [course]);
  const pct = useMemo(() => lessonPercentOf(course, progress), [course, progress]);

  /* ---------- the progress concern ---------- */
  /* The snapshot's load/migrate, owner-scoped persistence, gateway sync and
     every mutation live in `useProgressStore`. Its state (`progress`), refs and
     the shared `course`/`modules`/`activeLesson` stay here because other
     concerns read them: navigation starts from `progress`, the router reads
     `progressRef`, and sign-in resets `hydratedRef`/`dirtyRef`. The hook takes
     them in and returns the mutations, so `StoreValue` and every member keep
     their shape. */
  const {
    recordEvent,
    addNote,
    toggleBookmark,
    removeWorkbookItem,
    isBookmarked,
    saveQuery,
    removeSaved,
    addStudyTime,
    markLessonDone,
    loadSampleProgress,
    clearProgress,
    exportData,
    importData,
  } = useProgressStore({
    api,
    progress,
    setProgress,
    progressRef,
    ownerId,
    session,
    authReady,
    course,
    modules,
    activeLesson,
    selectedCourseId: uiState.selectedCourseId,
    toast,
    hydratedRef,
    dirtyRef,
    progressOwnerRef,
  });

  /* ---------- the hash router ---------- */
  /* The fragment ↔ state binding lives in `useHashRouter`. Its state and
     setters stay here because other concerns share them: auth and the run
     recovery set the screen, the course resolver fills `pendingCourse`, and
     progress reads `activeLesson`. The hook takes them in and returns only
     `showScreen`, so `StoreValue` and every member keep their shape. */
  const { showScreen } = useHashRouter({
    screen,
    challengeId,
    activeLesson,
    course,
    role,
    authReady,
    pendingCourse,
    uiState,
    uiStateRef,
    progressRef,
    saveUiState,
    setScreen,
    setChallengeId,
    setActiveLesson,
    setPendingCourse,
  });

  /* ---------- the catalog concern ---------- */
  /* The stored-course list, its mirror into `COURSES`, the fragment resolve,
     deletion, adoption and the `catalogVersion` re-render trigger live in
     `useCatalog`. The state and setters it reads stay here because other
     concerns share them: the router hands `pendingCourse` over, auth owns the
     session, and navigation owns the selected course and the active lesson.
     The hook takes them in and returns its members, so `StoreValue` and every
     member keep their shape. */
  const { catalogVersion, bumpCatalog, courseResolving, deleteCourse, adoptStoredCourse } = useCatalog({
    api,
    authReady,
    session,
    pendingCourse,
    setPendingCourse,
    setScreen,
    setActiveLesson,
    uiStateRef,
    saveUiState,
    toast,
  });

  /* ---------- the theme + motion concern ---------- */
  /* The device theme and motion preferences, the user-defined theme list, the
     account sync that carries them across devices, and the effects that apply
     them to the document live in `useTheme`. The hook owns that state because
     no other concern reads it; the provider passes in the API adapter and the
     session, which the account sync needs, and spreads the returned members
     into `StoreValue`, so the public shape and every member keep their shape. */
  const { theme, setTheme, customThemes, saveCustomTheme, deleteCustomTheme, motion, setMotion } = useTheme({
    api,
    authReady,
    session,
  });

  /* ---------- navigation ---------- */
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
    [sessionRef]
  );

  const runSettled = useCallback((jobId: string): void => {
    clearRun(jobId);
    setResumeRun((prev) => (prev && prev.jobId === jobId ? null : prev));
  }, []);

  const resumeStoredCourse = useCallback((courseId: string, goal: string): void => {
    setResumeBuild({ courseId, goal });
    setScreen('building');
  }, []);

  const buildSettled = useCallback((): void => {
    setResumeBuild(null);
  }, []);

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
    async ({ title }: { title?: string }) => {
      if (!canAuthor()) return;
      const next = title && title.trim() ? title.trim() : undefined;
      /* A stored course lives on the gateway, so the edit goes there first and
         the catalog changes only once it agreed. A built-in course has no row
         to write, so it keeps today's local-only edit. */
      if (course.gateway === true) {
        if (next === undefined) return;
        try {
          await api.updateCourse(course.id, { title: next });
        } catch (e) {
          toast(editFailureMessage(e));
          return;
        }
        course.title = next;
        bumpCatalog();
        toast('Course details saved.');
        return;
      }
      if (next !== undefined) COURSES[0].title = next;
      bumpCatalog();
      toast('Course details saved.');
    },
    [api, bumpCatalog, canAuthor, course, toast]
  );

  const saveLessonEdits = useCallback(
    async ({ title, summary }: { title?: string; summary?: string }) => {
      if (!canAuthor()) return;
      const m = activeModulesOf(course)[activeLesson];
      if (!m) return;
      const nextTitle = title && title.trim() ? title.trim() : undefined;
      const nextSummary = summary && summary.trim() ? summary.trim() : undefined;
      if (nextTitle === undefined && nextSummary === undefined) return;
      /* The gateway owns a stored course's chapters; the route edits them by
         chapter id, so a chapter the stored plan does not have is refused there
         rather than quietly diverging from the row. */
      if (course.gateway === true) {
        const edit: AiChapterEdit = { id: m.id };
        if (nextTitle !== undefined) edit.title = nextTitle;
        if (nextSummary !== undefined) edit.summary = nextSummary;
        try {
          await api.updateCourse(course.id, { chapters: [edit] });
        } catch (e) {
          toast(editFailureMessage(e));
          return;
        }
      }
      if (nextTitle !== undefined) m.title = nextTitle;
      if (nextSummary !== undefined) m.summary = nextSummary;
      bumpCatalog();
      toast('Chapter updated.');
    },
    [activeLesson, api, bumpCatalog, canAuthor, course, toast]
  );

  const applyExtension = useCallback(
    (kind: string, prompt?: string) => {
      if (!canAuthor()) return;
      const deeperPreset = ['Edge cases and advanced variants', 'Add harder exercises with ties, NULLs, missing periods, duplicate-producing joins, and performance trade-offs.'];
      const PRESETS = new Map<string, string[]>([
        ['deeper', deeperPreset],
        ['advanced', ['Advanced SQL patterns', 'Add advanced modules on lateral joins, recursive CTEs, percentile analysis, and efficient analytical queries.']],
        ['project', ['Integrated capstone project', 'Add an open-ended marketplace analytics project with staged milestones, tests, and a final review.']],
        ['branch', ['Query planning and indexing branch', 'Add an optional branch on EXPLAIN ANALYZE, index design, cardinality estimates, and query rewrites.']],
        ['practice', ['Interview practice set', 'Add a graduated set of interview problems with hidden edge cases and explanation after submission.']],
        ['context', ['Domain-specific adaptation', 'Adapt exercises to a domain-specific schema and business rules supplied by the learner.']],
      ]);
      const p = PRESETS.get(kind) ?? deeperPreset;
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
    ({ title, goal }: { title: string; goal?: string }) => {
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

  /* The provider value, rebuilt only when something in it changed. Without
     this, one query or one toast rebuilt the object and re-rendered every
     `useStore()` consumer (34 call sites across 25 files). The five derived
     values above are memoized on their real inputs so this list stays honest:
     `catalogVersion` is the re-render trigger for the catalog mutations, which
     edit `COURSES`/`MODULES` in place rather than replacing them. */
  const value = useMemo(
    () => ({
    // data
    COURSES,
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
    reviewTab,
    setReviewTab,
    tablesOpen,
    setTablesOpen,
    tablesFocus,
    openTables,
    tutorTarget,
    openTutor,
    closeTutor,
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
    setTheme,
    customThemes,
    saveCustomTheme,
    deleteCustomTheme,
    motion,
    setMotion,
    // navigation
    openLessonIn,
    selectCourse,
    enrollCourse,
    resumeCourse,
    startLearning,
    stepLesson,
    markLessonDone,
    // progress
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
    content,
    moveLesson,
    saveCourseEdits,
    saveLessonEdits,
    applyExtension,
    addLesson,
    addSection,
    // create/build/review draft
    goalInput,
    reviewGoal,
    setReviewGoal,
    courseSubtitle,
    contextState,
    submitBuild,
    useAuthoredDraft,
    adoptGeneratedCourse,
    draftReport,
    setDraftReport,
    buildContext,
    // M8 stored courses
    courseResolving,
    deleteCourse,
    adoptStoredCourse,
    resumeRun,
    runStarted,
    runSettled,
    resumeBuild,
    resumeStoredCourse,
    buildSettled,
    // misc
    }),
    [
      activeLesson, addLesson, addNote, addSection, addStudyTime, adminPasswordEmpty, adoptGeneratedCourse, adoptStoredCourse,
      aiAvailability, api, apiCount, apiLog, applyExtension, authNotice, authReady, buildContext, buildSettled, catalogVersion,
      challengeId, chaptersOpen, clearProgress, content, contextState, course, courseResolving, courseSubtitle, customThemes,
      deleteCourse, deleteCustomTheme, draftCourse, draftReport, engineCaps, enrollCourse, exportData, goalInput, importData,
      isBookmarked, latency, loadSampleProgress, login, logout, markLessonDone, modal, modules, motion, moveLesson, navOpen,
      openBookmark, openLessonIn, openSearchHit, pct, progress, promptRequest, recordEvent, removeSaved, removeWorkbookItem,
      resumeBuild, resumeCourse, resumeRun, resumeStoredCourse, reviewGoal, reviewTab, role, runSettled, runStarted,
      saveCourseEdits, saveCustomTheme, saveLessonEdits, saveQuery, screen, selectCourse, session, setDraftReport, setMotion, setReviewGoal, setTheme, showScreen,
      signup, startLearning, stepLesson, submitBuild, tablesOpen, tablesFocus, openTables, theme, toast, toastMsg, toggleBookmark, uiState, useAuthoredDraft,
      tutorTarget, openTutor, closeTutor,
    ]
  );

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}

export function useStore() {
  const ctx = useContext(StoreContext);
  if (!ctx) throw new Error('useStore must be used within StoreProvider');
  return ctx;
}
