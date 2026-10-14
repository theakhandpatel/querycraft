import React, { useEffect } from 'react';
import { IconSprite } from './components/ui/Icon';
import Toast from './components/ui/Toast';
import PromptDialog from './components/ui/PromptDialog';
import { Sidebar, Header } from './components/Shell';
import TablesDrawer from './components/TablesDrawer';
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
import type { Screen } from './types';

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
     routes (stale nav, deep link) is sent back to Home rather than shown it. */
  const AUTHORING: Screen[] = ['create', 'building', 'review'];
  const Active = s.role !== 'admin' && AUTHORING.includes(s.screen) ? HomeScreen : SCREENS[s.screen] || HomeScreen;

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        if (s.modal) s.closeModal();
        if (s.tablesOpen) s.setTablesOpen(false);
      }
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [s]);

  /* Hold the first paint until we know who is signed in, so a restored session
     never flashes the sign-in screen on reload. */
  if (!s.authReady) {
    return (
      <>
        <IconSprite />
        <div className="authwrap"><span className="spin" role="status" aria-label="Restoring session" /></div>
      </>
    );
  }

  /* No session (gateway or mock mode, signed out): the app is gated behind
     sign-in. The offline PGlite adapter always has a local workspace session,
     so it renders straight through. */
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
        {/* First stop on the page: thirteen sidebar controls separate the top of
            the document from the chapter, and a keyboard should not have to walk
            past all of them every time. */}
        <a className="skiplink" href="#main">Skip to the lesson content</a>
        <Sidebar />
        <div className="shell">
          <Header />
          <main className="workspace" id="main" tabIndex={-1}>
            <section className="screen active" key={s.screen}>
              <Active />
            </section>
          </main>
        </div>
      </div>
      <TablesDrawer />
      <Modals />
      <PromptDialog />
      <Toast />
    </>
  );
}
