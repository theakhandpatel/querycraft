/*
 * The theme and motion concern as a hook. `lib/themes.ts` owns the shipped
 * preset registry and `lib/userThemes.ts` owns the derivation and the DOM
 * injection; this module binds them to the store: the device-local theme and
 * motion preferences, the user-defined theme list, the account sync that
 * carries both across devices, and the effects that apply the result to the
 * document (`data-theme` and `data-motion`).
 *
 * The state it moves stays here, because no other concern reads it. What stays
 * in `StoreProvider` is only what the hook reads: the API adapter and the
 * session, because the account preference is fetched and written through them.
 * The provider passes them in and spreads the returned members into
 * `StoreValue`, so the public shape and each member's behaviour are unchanged.
 * This is the same reason the router's, progress's and catalog's own state
 * stayed behind in their hooks' providers.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { injectUserThemes } from '../lib/userThemes';
import type { UserTheme } from '../lib/userThemes';
import { loadJSON, saveJSON } from './jsonStore';
import type { AuthSession, ExecutionApi, ThemeName, ThemePreference } from '../types';

const THEME_KEY = 'qc.theme';
const MOTION_KEY = 'qc.motion';
const CUSTOM_THEMES_KEY = 'qc.customThemes';

/** A string preference read from the gateway, before a schema reads it. */
function isStringPreference(value: unknown): value is string {
  return typeof value === 'string';
}

/** The provider state the hook reads. Every field is owned by the provider;
 *  the hook neither holds nor replaces any of it. */
interface ThemeStoreOptions {
  api: ExecutionApi;
  /** False until the session is restored; the account sync waits for it. */
  authReady: boolean;
  /** Null when signed out; the account preference belongs to a real account. */
  session: AuthSession | null;
}

/** The theme and motion members the provider spreads into `StoreValue`. */
interface ThemeStore {
  theme: ThemePreference;
  customThemes: UserTheme[];
  saveCustomTheme: (t: UserTheme) => void;
  deleteCustomTheme: (id: string) => void;
  setTheme: (m: ThemePreference) => void;
  motion: boolean;
  setMotion: () => void;
}

export function useTheme({ api, authReady, session }: ThemeStoreOptions): ThemeStore {
  const [theme, setThemeState] = useState<ThemePreference>(() => loadJSON<ThemePreference>(THEME_KEY, 'ledger'));
  const [motion, setMotionState] = useState<boolean>(() => loadJSON<boolean>(MOTION_KEY, false));

  /* User-defined themes: their token blocks are injected as a style element,
     so any id resolves the moment the data-theme attribute names it. The
     list itself is state (rendered by the Appearance picker) and is synced
     to the account with the theme preference. */
  const [customThemes, setCustomThemes] = useState<UserTheme[]>(() => loadJSON<UserTheme[]>(CUSTOM_THEMES_KEY, []));
  /* The sync saves in setTheme read this ref, not the state variable: a save
     and a theme change fire in the same handler, and the state variable would
     still hold the previous render's array. The ref is updated synchronously
     by every mutation. */
  const customThemesRef = useRef<UserTheme[]>(customThemes);
  useEffect(() => {
    injectUserThemes(customThemes);
    saveJSON(CUSTOM_THEMES_KEY, customThemes);
  }, [customThemes]);

  const effectiveTheme = useMemo<ThemePreference>(
    () => (theme === 'system' ? (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'ledger') : theme),
    [theme]
  );
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', effectiveTheme);
  }, [effectiveTheme]);
  /* The account's preference outranks the device's localStorage once it is
     known: the theme follows the learner across devices. The offline adapter
     answers null and the device choice stands. */
  useEffect(() => {
    if (api.mode !== 'http' || !authReady || !session) return;
    let alive = true;
    void api.preferences().then((prefs) => {
      if (!alive || !prefs) return;
      const savedTheme = isStringPreference(prefs.theme) ? prefs.theme : null;
      const savedCustom = Array.isArray(prefs.customThemes) ? (prefs.customThemes as UserTheme[]) : null;
      if (savedCustom) { customThemesRef.current = savedCustom; setCustomThemes(savedCustom); }
      /* The account value is applied through a functional update, so the effect
         does not need `theme` as a dependency and cannot clobber a theme the
         learner changed while the gateway call was in flight. */
      if (savedTheme) {
        saveJSON(THEME_KEY, savedTheme);
        setThemeState((current) => (savedTheme === current ? current : (savedTheme as ThemePreference)));
      }
    }).catch(() => { /* the gateway could not be asked; the device choice stands */ });
    return () => { alive = false; };
  }, [api, authReady, session]);
  useEffect(() => {
    const mq = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
    if (!mq) return;
    const fn = () => {
      if (loadJSON<ThemeName>(THEME_KEY, 'ledger') === 'system') document.documentElement.setAttribute('data-theme', mq.matches ? 'dark' : 'ledger');
    };
    if (mq.addEventListener) mq.addEventListener('change', fn);
    else mq.addListener(fn);
    return () => (mq.removeEventListener ? mq.removeEventListener('change', fn) : mq.removeListener(fn));
  }, []);
  useEffect(() => {
    if (motion) document.documentElement.setAttribute('data-motion', 'reduced');
    else document.documentElement.removeAttribute('data-motion');
  }, [motion]);

  const setTheme = useCallback(
    (m: ThemePreference) => {
      setThemeState(m);
      saveJSON(THEME_KEY, m);
      // The preference follows the account: written to the gateway when one
      // is attached, in addition to the device's localStorage above.
      if (api.mode === 'http' && session) {
        void api.savePreferences({ theme: m, customThemes: customThemesRef.current }).catch(() => { /* device keeps it */ });
      }
    },
    [api, session]
  );

  const saveCustomTheme = useCallback(
    (t: UserTheme) => {
      const next = customThemesRef.current.some((x) => x.id === t.id)
        ? customThemesRef.current.map((x) => (x.id === t.id ? t : x))
        : [...customThemesRef.current, t];
      customThemesRef.current = next;
      setCustomThemes(next);
      saveJSON(CUSTOM_THEMES_KEY, next);
      if (api.mode === 'http' && session) {
        void api.savePreferences({ theme, customThemes: next }).catch(() => { /* device keeps it */ });
      }
    },
    [api, session, theme]
  );

  const deleteCustomTheme = useCallback(
    (id: string) => {
      const next = customThemesRef.current.filter((x) => x.id !== id);
      customThemesRef.current = next;
      setCustomThemes(next);
      saveJSON(CUSTOM_THEMES_KEY, next);
      if (api.mode === 'http' && session) {
        void api.savePreferences({ theme, customThemes: next }).catch(() => { /* device keeps it */ });
      }
    },
    [api, session, theme]
  );

  const setMotion = useCallback(() => {
    const next = !motion;
    setMotionState(next);
    saveJSON(MOTION_KEY, next);
  }, [motion]);

  return { theme, customThemes, saveCustomTheme, deleteCustomTheme, setTheme, motion, setMotion };
}
