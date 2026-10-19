/* ---------- signed-in account storage ----------

   The gateway holds the session in an `HttpOnly`, `SameSite=Lax` cookie
   (`qc_session`), so the page never sees or stores the credential. The only
   thing kept here is the signed-in account id, a non-secret convenience the AI
   seam echoes as `approved_by`. The gateway still attributes every write to the
   session cookie, so the stored id is never an authority. Every access is
   guarded: prerender/SSR (no `window`) and disabled storage both degrade to "no
   account", never a crash.

   The store lives in its own module, free of any engine or dataset import, so
   the AI seam can read the signed-in account without pulling the browser
   engine into its bundle. `execution/shared.ts` re-exports these for the
   callers that already import them from there. */

/* The signed-in account id, kept beside the cookie session. It is what a patch
   approval echoes as `approved_by`; the gateway still attributes the approval to
   the session cookie, so this is a convenience, never an authority. */
export const AUTH_USER_KEY = 'querycraft.user';

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
    /* storage disabled: the account marker lives for this page only */
  }
}

export function clearAuthUser(): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return;
    window.localStorage.removeItem(AUTH_USER_KEY);
  } catch {
    /* storage disabled */
  }
}
