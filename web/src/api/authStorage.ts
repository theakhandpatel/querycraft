/* ---------- bearer token storage ----------

   The gateway's auth surface issues one bearer token; it is the only
   credential the client ever sends. It lives under a single localStorage key so
   `httpLayer` can attach it and `me`/`logout` can clear it. Every access is
   guarded: prerender/SSR (no `window`) and disabled storage both degrade to "no
   token", never a crash.

   The store lives in its own module, free of any engine or dataset import, so
   the AI seam can read the signed-in account without pulling the browser
   engine into its bundle. `execution/shared.ts` re-exports these for the
   callers that already import them from there. */

export const AUTH_TOKEN_KEY = 'querycraft.token';
/* The signed-in account id, kept beside the token. It is what a patch approval
   echoes as `approved_by`; the gateway still attributes the approval to the
   bearer token, so this is a convenience, never an authority. */
export const AUTH_USER_KEY = 'querycraft.user';

export function readAuthToken(): string | null {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return null;
    return window.localStorage.getItem(AUTH_TOKEN_KEY);
  } catch {
    return null;
  }
}

export function writeAuthToken(token: string): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.setItem(AUTH_TOKEN_KEY, token);
  } catch {
    /* storage disabled: the session lives for this page only */
  }
}

export function readAuthUser(): string | null {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return null;
    return window.localStorage.getItem(AUTH_USER_KEY);
  } catch {
    return null;
  }
}

export function writeAuthUser(userId: string): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.setItem(AUTH_USER_KEY, userId);
  } catch {
    /* storage disabled: the session lives for this page only */
  }
}

export function clearAuthToken(): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.removeItem(AUTH_TOKEN_KEY);
    window.localStorage.removeItem(AUTH_USER_KEY);
  } catch {
    /* storage disabled */
  }
}
