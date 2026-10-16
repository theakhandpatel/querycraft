import { useEffect } from 'react';
import { IconSprite } from './components/ui/Icon';
import Toast from './components/ui/Toast';
import PromptDialog from './components/ui/PromptDialog';
import { TopBar, NavDrawer } from './components/Shell';
import TablesDrawer from './components/TablesDrawer';
import ChaptersDrawer from './components/ChaptersDrawer';
import Modals from './components/Modals';
import HomeScreen from './components/screens/HomeScreen';
import CourseScreen from './components/screens/CourseScreen';
import LibraryScreen from './components/screens/LibraryScreen';
import CreateScreen from './components/screens/CreateScreen';
import BuildingScreen from './components/screens/BuildingScreen';
import ReviewScreen from './components/screens/ReviewScreen';
import LessonScreen from './components/screens/LessonScreen';
import WorkbookScreen from './components/screens/WorkbookScreen';
import ProgressScreen from './components/screens/ProgressScreen';
import ChallengeLabScreen from './components/screens/ChallengeLabScreen';
import AppearanceScreen from './components/screens/AppearanceScreen';
import AuthScreen from './components/screens/AuthScreen';
import { useStore } from './state/store';
import { AUTHORING_SCREENS } from './lib/route';

const SCREENS = {
  home: HomeScreen,
  course: CourseScreen,
  library: LibraryScreen,
  create: CreateScreen,
  building: BuildingScreen,
  review: ReviewScreen,
  lesson: LessonScreen,
  workbook: WorkbookScreen,
  'workbook-progress': ProgressScreen,
  'challenge-lab': ChallengeLabScreen,
  'appearance-settings': AppearanceScreen,
};

export default function App() {
  const s = useStore();
  /* Authoring lives behind the admin role; a learner landing on one of those
     routes (stale nav, deep link) is sent back to Home rather than shown it.
     The router applies the same list to the fragment. */
  const Active = s.role !== 'admin' && AUTHORING_SCREENS.includes(s.screen) ? HomeScreen : SCREENS[s.screen] || HomeScreen;

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        /* One layer per press, topmost first: the modal outranks the tables
           drawer, which outranks the chapter list, which outranks the mobile
           navigation. The search field closes its own panel and stops the
           event before it reaches here. */
        if (s.modal) s.closeModal();
        else if (s.tablesOpen) s.setTablesOpen(false);
        else if (s.chaptersOpen) s.setChaptersOpen(false);
        else if (s.navOpen) s.setNavOpen(false);
      }
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [s]);

  /* Hold the first paint until we know who is signed in — and, when the link
     named a course the catalog does not have, until that course has been
     fetched from the gateway. Painting before either would show the wrong
     screen and then correct itself. */
  if (!s.authReady || s.courseResolving) {
    return (
      <>
        <IconSprite />
        <div className="authwrap">
          <span className="spin" role="status" aria-label={s.authReady ? 'Opening your course' : 'Restoring session'} />
        </div>
      </>
    );
  }

  /* No session (gateway mode, signed out): the app is gated behind sign-in.
     The offline PGlite adapter always has a local workspace session, so it
     renders straight through. */
  if (!s.session) {
    return (
      <>
        <IconSprite />
        <AuthScreen />
        <Toast />
      </>
    );
  }

  return (
    <>
      <IconSprite />
      <div className="app">
        {/* First stop on the page: the top bar's controls separate the top of
            the document from the chapter, and a keyboard should not have to walk
            past all of them every time. */}
        <a className="skiplink" href="#main">Skip to content</a>
        <TopBar />
        <main className="workspace" id="main" tabIndex={-1}>
          <section className="screen active" key={s.screen}>
            <Active />
          </section>
        </main>
      </div>
      <TablesDrawer />
      <ChaptersDrawer />
      <NavDrawer />
      <Modals />
      <PromptDialog />
      <Toast />
    </>
  );
}
