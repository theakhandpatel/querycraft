import React, { useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';

/* Sign-in / sign-up against the gateway's auth surface, in one screen with a
   toggle. All calls go through the store (and therefore the ExecutionService
   seam); this component never touches fetch. */

type Mode = 'login' | 'signup';

/** Turn a thrown auth error into a line the learner can act on. */
// oxlint-disable-next-line anti-slop/no-unknown-parameters -- a thrown auth failure is untrusted; this reads its typed code.
function describeAuthError(e: unknown): string {
  const err = e as { code?: string; message?: string } | null;
  switch (err?.code) {
    case 'AUTH_INVALID_CREDENTIALS':
      return 'That email and password do not match.';
    case 'AUTH_EMAIL_TAKEN':
      return 'That email already has an account. Try signing in instead.';
    case 'AUTH_RATE_LIMITED':
      /* The gateway throttles failed sign-ins per address+account. The copy has
         to say what happened and what to do, because "email or password is
         incorrect" would be a lie — the credentials may be fine, the caller is
         simply out of attempts for a moment. */
      return 'Too many failed sign-ins. Wait a few minutes and try again.';
    case 'AUTH_INVALID_INPUT':
      return err?.message || 'Check the email, name, and password and try again.';
    case 'AUTH_UNAVAILABLE':
      return 'Accounts need the Querycraft gateway. The offline engine cannot sign anyone in.';
    case 'ENVIRONMENT_UNAVAILABLE':
      return 'Could not reach the gateway. Check that it is running and try again.';
    default:
      return err?.message || 'Something went wrong. Try again.';
  }
}

interface FieldErrors {
  email?: string;
  name?: string;
  password?: string;
}

export default function AuthScreen() {
  const s = useStore();
  const { authNotice } = s;
  const [mode, setMode] = useState<Mode>('login');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [busy, setBusy] = useState(false);

  function validate(): FieldErrors {
    const next: FieldErrors = {};
    if (!email.trim() || !email.includes('@')) next.email = 'Enter a valid email address.';
    if (mode === 'signup') {
      if (!name.trim()) next.name = 'Enter your name.';
      /* Only the signup form enforces a minimum; an empty password is valid on
         sign-in (the seeded local admin has none). */
      if (password.length < 8) next.password = 'Use at least 8 characters.';
    }
    return next;
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    const problems = validate();
    setFieldErrors(problems);
    if (Object.keys(problems).length) return;
    setError(null);
    setBusy(true);
    try {
      if (mode === 'signup') {
        await s.signup({ email: email.trim(), name: name.trim(), password });
      } else {
        await s.login({ email: email.trim(), password });
      }
    } catch (err) {
      setError(describeAuthError(err));
    } finally {
      setBusy(false);
    }
  }

  function switchMode(next: Mode) {
    if (next === mode) return;
    setMode(next);
    setError(null);
    setFieldErrors({});
  }

  return (
    <div className="authwrap">
      <div className="authcard">
        <div className="brand" style={{ padding: 0 }}>
          <span className="brandmark" aria-hidden="true" />
          <span className="brandname">Querycraft</span>
        </div>

        <div className="authtabs" role="tablist">
          <button
            role="tab"
            aria-selected={mode === 'login'}
            className={`authtab${mode === 'login' ? ' active' : ''}`}
            onClick={() => switchMode('login')}
          >
            Sign in
          </button>
          <button
            role="tab"
            aria-selected={mode === 'signup'}
            className={`authtab${mode === 'signup' ? ' active' : ''}`}
            onClick={() => switchMode('signup')}
          >
            Create account
          </button>
        </div>

        <h1 className="authtitle">{mode === 'login' ? 'Welcome back' : 'Create your account'}</h1>
        <p className="muted authtagline">
          {mode === 'login'
            ? 'Sign in to keep your progress, notes, and worked answers under your account.'
            : 'Your progress and events are scoped to your account on the server.'}
        </p>

        {authNotice && <div className="authinfo">{authNotice}</div>}
        {error && <div className="autherr" role="alert"><Icon name="alert" />{error}</div>}

        <form onSubmit={submit} noValidate>
          {mode === 'signup' && (
            <div className="field">
              <label htmlFor="auth-name">Name</label>
              <input
                id="auth-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Ada Lovelace"
                autoComplete="name"
              />
              {fieldErrors.name && <span className="authfielderr">{fieldErrors.name}</span>}
            </div>
          )}
          <div className="field">
            <label htmlFor="auth-email">Email</label>
            <input
              id="auth-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
              autoComplete="email"
            />
            {fieldErrors.email && <span className="authfielderr">{fieldErrors.email}</span>}
          </div>
          <div className="field">
            <label htmlFor="auth-password">Password</label>
            <input
              id="auth-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={mode === 'signup' ? 'At least 8 characters' : 'Your password'}
              autoComplete={mode === 'signup' ? 'new-password' : 'current-password'}
            />
            {fieldErrors.password && <span className="authfielderr">{fieldErrors.password}</span>}
          </div>

          <button className="primary authtsubmit" type="submit" disabled={busy}>
            {busy ? <span className="spin" /> : <Icon name="arrow-right" />}
            {busy ? 'Working…' : mode === 'login' ? 'Sign in' : 'Create account'}
          </button>
        </form>

        {/* The seeded local administrator is a local-development convenience,
            so the hint is shown only when the gateway itself reports that the
            account really has an empty password (a loopback-only install). A
            release that set QC_ADMIN_PASSWORD, or a gateway reached over the
            network, reports false and no hint is shown. */}
        {s.adminPasswordEmpty && (
          <div className="authhint">
            <Icon name="shield" />
            <span>
              Local admin: <span className="mono">admin@querycraft.local</span> with an empty password.
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
