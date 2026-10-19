/*
 * The auth and session concern as a hook. `api/authStorage.ts` owns the
 * non-secret account-id storage; the session itself rides the gateway's
 * same-origin `qc_session` cookie, which the page cannot read. This module
 * binds the account lifecycle to the store: the session restored by asking the
 * gateway who the cookie names at boot, whether that restore has finished
 * (`authReady`), the expired-session notice, the signed-in account's `role`,
 * the signup/login/logout actions and the 401 reaction.
 *
 * The state and the `sessionRef` it moves stay here, because no other concern
 * owns them. The two pieces of cross-concern wiring the actions trigger stay in
 * `StoreProvider` and are passed in: the router's screen (`goHome`) and the
 * progress hydration guards (`resetHydration`). The provider passes them in and
 * spreads the returned members into `StoreValue`, so the public shape and each
 * member's behaviour are unchanged. This is the same reason the router's,
 * progress's, catalog's and theme's own state stayed behind in their hooks'
 * providers.
 */

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { MutableRefObject } from 'react';
import type { AuthSession, ExecutionApi, LoginInput, SignupInput, UserRole } from '../types';

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

/** The provider state the hook reads and the cross-concern callbacks it calls.
 *  Every field is owned by the provider; the hook neither holds nor replaces
 *  any of it. */
interface AuthStoreOptions {
  api: ExecutionApi;
  /** Send the learner back to Home. The screen state lives in the provider's
      router, and a sign-in, a sign-out and an expired session all land there. */
  goHome: () => void;
  /** The account whose workspace is loaded is about to change (signup, login or
      logout). The provider resets the progress hydration guards for it; the 401
      path does not call this, because the owner swap already resets them. */
  resetHydration: () => void;
}

/** The auth members the provider spreads into `StoreValue`. */
export interface AuthStore {
  /** Null when signed out (the app then shows the auth screen in gateway mode). */
  session: AuthSession | null;
  /** The session as the stable callbacks need it; they are identity-stable and
      must not close over the session of the first render. */
  sessionRef: MutableRefObject<AuthSession | null>;
  /** False until the stored session has been restored, so App waits for it. */
  authReady: boolean;
  /** The expired-session sentence, or null. */
  authNotice: string | null;
  /** The account's role, gating the authoring surfaces. */
  role: UserRole;
  signup: (input: SignupInput) => Promise<void>;
  login: (input: LoginInput) => Promise<void>;
  logout: () => Promise<void>;
  /** The reaction to a 401 with AUTH_REQUIRED from any gateway request. */
  onUnauthorized: () => void;
}

export function useAuth({ api, goHome, resetHydration }: AuthStoreOptions): AuthStore {
  const [session, setSession] = useState<AuthSession | null>(null);
  const sessionRef = useRef<AuthSession | null>(null);
  /* The stable callbacks read the session through this ref rather than closing
     over it. A layout effect writes it after commit, before any callback can
     fire, and a discarded render cannot move it. */
  useLayoutEffect(() => {
    sessionRef.current = session;
  }, [session]);
  const [authReady, setAuthReady] = useState(false);
  const [authNotice, setAuthNotice] = useState<string | null>(null);

  /* A 401 with AUTH_REQUIRED from any gateway request means the session is gone
     or expired: drop the session and send the learner back to sign-in. The http
     layer fires this; the offline adapters never do. */
  const onUnauthorized = useCallback(() => {
    setSession(null);
    setAuthNotice('Your session expired. Sign in to continue.');
    goHome();
  }, [goHome]);

  /* Restore the session once, before the app decides what to render.
   *
   * The offline engine has no auth server, so it runs as a local workspace. The
   * gateway answers from the `qc_session` cookie, which this page cannot read;
   * no account means the auth screen.
   * `me()` resolves to null rather than failing on a missing/expired session,
   * so this is a plain "who am I" check. */
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
      /* The cookie is the credential, so the in-memory session carries no
         token; the account alone is what the UI renders. */
      setSession(user ? { user, token: '', expiresAt: '' } : null);
      setAuthReady(true);
    };
    void restore();
    return () => {
      alive = false;
    };
  }, [api]);

  /* The gateway starts the session and sets the cookie; the actions track which
     account is signed in and let failures (bad credentials, taken email) reach
     the form. A sign-in or sign-out changes the workspace owner, so the
     provider resets the progress hydration guards and the router returns Home. */
  const signup = useCallback(
    async (input: SignupInput) => {
      const next = await api.signup(input);
      resetHydration();
      setAuthNotice(null);
      setSession(next);
      goHome();
    },
    [api, resetHydration, goHome]
  );
  const login = useCallback(
    async (input: LoginInput) => {
      const next = await api.login(input);
      resetHydration();
      setAuthNotice(null);
      setSession(next);
      goHome();
    },
    [api, resetHydration, goHome]
  );
  const logout = useCallback(async () => {
    try {
      /* The gateway clears the cookie and revokes the server session, so a
         later request cannot use it even if this page crashes. */
      await api.logout();
    } catch {
      /* clearing the local session matters more than the revoke round trip */
    }
    resetHydration();
    /* The offline engine has no session to end: stay on the local workspace. */
    setSession(api.mode === 'pglite' ? LOCAL_SESSION : null);
    goHome();
  }, [api, resetHydration, goHome]);

  /* The account's role, read once: the router gates the authoring screens with
     the same rule App renders behind. */
  const role = useMemo<UserRole>(() => session?.user.role ?? 'learner', [session]);

  return { session, sessionRef, authReady, authNotice, role, signup, login, logout, onUnauthorized };
}
