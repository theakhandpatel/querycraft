# ADR 0009: The SPA authenticates with an HttpOnly session cookie

- **Status:** accepted
- **Date:** 2026-10-19
- **Deciders:** project owner + agent session
- **Question:** the web client kept the session token in `localStorage` and sent
  it as `Authorization: Bearer`. Any script injected into the page could read
  the token and impersonate the learner until it expired. How should the SPA
  carry its session without holding the secret in script-reachable storage?

Settles the `localStorage` gap recorded in `docs/features.md` section 10. Keeps
the bearer transport for API clients, which item 19-i added the cookie beside.

## Context

The gateway issues one opaque session token on signup and login. The SPA wrote
it to `localStorage` and attached it to every request. The token is a bearer
credential: whoever holds it is the account. `localStorage` is readable by any
script on the origin, so one cross-site scripting bug leaks a working session,
and there is no separate secret an attacker would also need.

The browser already has a transport built for this. A cookie marked `HttpOnly`
is invisible to script, and `SameSite=Lax` keeps the browser from attaching it
to a cross-site request. The trade is that the SPA can no longer read the token,
so it cannot decide on its own whether it is signed in; it has to ask.

## Decision

1. **The SPA authenticates with the `qc_session` cookie.** The gateway sets it
   on signup and login (`HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` over
   TLS) and clears it on logout. The web client sends no `Authorization` header
   and sends `credentials: 'same-origin'` on every fetch.
2. **Cookie mutations are same-origin only.** A request that authenticates from
   the cookie and mutates state is refused with `403 CSRF_REFUSED` when its
   `Origin` or `Sec-Fetch-Site` names another site. A request with neither
   header is a non-browser client that had to attach the cookie itself, so it is
   allowed. `SameSite=Lax` is the first layer; the same-origin check is the
   second.
3. **The bearer token stays for API clients.** Signup and login still return
   `token`, and the gateway still accepts `Authorization: Bearer`. The header
   wins over the cookie, and a present-but-invalid token never falls back to the
   cookie. Scripts, the smoke test and the audits keep a stable contract.
4. **The client keeps only the account id.** `authStorage.ts` drops the token
   key and stores the signed-in account id alone, as the echo a patch approval
   needs for `approved_by`. That id is never an authority: the gateway
   attributes every write to the session.
5. **Boot restore asks the gateway.** `me()` runs on every load, because the
   cookie may name a session the page cannot see. A 401 means "signed out", an
   answer is the account. No local token means no early return.
6. **Cross-origin cookie auth is out of scope.** `SameSite=Lax` withholds the
   cookie on a cross-site request, and `Access-Control-Allow-Origin: *` cannot
   carry credentials. The SPA runs same-origin, which the single binary already
   provides.

## Consequences

- One cross-site scripting bug no longer hands over a reusable session. The
  script can still act as the learner while the page is open, so XSS stays a
  real bug; the token is no longer the thing it steals.
- The token no longer reaches `localStorage`, so it cannot survive the page and
  cannot leak through a later export or a shared device.
- A new XSS class appears in theory: script can call the API as the learner. The
  defense is the same as before, which is to prevent the injection.
- The Vite dev flow that points the SPA at a gateway on another port cannot
  authenticate by cookie: a cross-origin fetch sends no credentials and the
  wildcard CORS policy accepts none. Serve the SPA from the gateway origin, as
  the single binary does.
- The smoke test drives the browser-shaped flow with a cookie jar (`curl -c` /
  `-b`) and keeps one bearer request so the API-client contract stays pinned.

## Alternatives considered

- **Keep the token in `localStorage`.** Simplest, and it was the status quo. It
  leaves the session readable by any script, which is the flaw this ADR exists
  to remove. Rejected.
- **Cookie for every client, including scripts.** Removes the bearer surface but
  forces a cookie jar on every script and audit, and a non-browser client has no
  origin for the CSRF check to read. Rejected.
- **A second cookie the script reads, compared to the session cookie
  (double-submit).** The script still reads a token, so it adds a moving part
  without removing the exposure. Rejected.
- **A per-request anti-CSRF header.** Works, and every mutating call would have
  to mint and verify one. `SameSite=Lax` plus the same-origin check covers the
  browser path without a new secret to store. Rejected for now.
