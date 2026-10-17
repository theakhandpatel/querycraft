import Icon from '../ui/Icon';
import { useState } from 'react';
import { THEMES } from '../../lib/themes';
import { buildUserTheme, deriveUserTokens, type UserTheme } from '../../lib/userThemes';
import type { ThemePreference } from '../../types';
import { useStore } from '../../state/store';
import ImportControl from '../ui/ImportControl';


/* A switch is a div, so it carries no name of its own. `labelledBy` points at
   the visible label beside it, which keeps one copy of the words. */
function Switch({ labelledBy, on, onClick }: { labelledBy: string; on: boolean; onClick: () => void }) {
  return <div className="switch" role="switch" aria-checked={on} aria-labelledby={labelledBy} tabIndex={0} onClick={onClick} onKeyDown={(e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); onClick(); } }}></div>;
}


export default function AppearanceScreen() {
  const s = useStore();
  /* The creator state: a name, an accent, and the base family the neutral
     tokens come from. Saving derives the full token set (the same contrast
     math the shipped presets pass) and applies it immediately. */
  const [form, setForm] = useState({ name: '', accent: '#7da7ff', base: 'light' as 'light' | 'dark' });
  const [editing, setEditing] = useState<{ id: string; name: string; accent: string; base: 'light' | 'dark' } | null>(null);

  function saveCustom() {
    const name = (editing ? editing.name : form.name).trim() || 'My theme';
    const accent = (editing ? editing.accent : form.accent).trim();
    const base = editing ? editing.base : form.base;
    const theme: UserTheme = editing
      ? { ...editing, name, accent, tokens: deriveUserTokens(accent, base) }
      : buildUserTheme(name, accent, base);
    s.saveCustomTheme(theme);
    s.setTheme(theme.id as ThemePreference);
    setEditing(null);
    setForm({ name: '', accent: '#7da7ff', base: 'light' });
  }

  const last = s.apiLog[0];
  /* Report what is actually behind the surface for the active adapter. */
  const engineLine =
    s.api.mode === 'http' ? s.api.baseUrl
    : s.api.mode === 'pglite' ? 'POST /query → PostgreSQL 18 · PGlite (in-browser)'
    : 'POST /query → PostgreSQL 16.3 (simulated)';
  const storageLine =
    s.api.mode === 'http' ? 'PUT /progress/:course → gateway (PostgreSQL)'
    : 'PUT /progress/:course → IndexedDB (this browser)';
  /* §5.2: the deadline is a host capability, not a promise. A page that blocks
     Web Workers runs the engine on the main thread, where nothing can stop a
     runaway statement — say that instead of printing a deadline that is not
     enforced. Unknown until the engine has booted. */
  const deadlineLine =
    s.engineCaps?.placement === 'main'
      ? 'hard_timeout off — this page blocks Web Workers'
      : s.engineCaps?.placement === 'none'
        ? 'hard_timeout n/a — canned results'
        : 'hard_timeout 5s (host-enforced)';
  /* The isolation row was a hardcoded "rolled-back transaction per query",
     which is false for the default PGlite workspace and for every signed-in
     gateway learner — both are writable by design (ADR 0003/0006). Derive it
     from the capability the adapter actually reports, and say "unknown" rather
     than guessing before the engine has booted. */
  const isolationLine =
    !s.engineCaps
      ? 'checking…'
      : s.engineCaps.writable
        ? 'writable workspace — your own database; reset restores the fixture'
        : 'read-only — the shared dataset; writes are refused';
  return (
    <div className="content" style={{ maxWidth: 760 }}>
      <div className="pagehead">
        <div>
          <div className="eyebrow">Appearance</div>
          <h1>Theme &amp; reading</h1>
          <p>Light and dark are one token set with two palettes. Default is light, ink on paper with a pine accent.</p>
        </div>
      </div>

      <div className="card">
        <h2>Theme</h2>
        <p className="mt-8">
          Choose the palette. System follows your OS setting. Every preset —
          and every theme you make — clears 4.5:1 on its key text pairs; the
          derivation tunes the inks itself. Your theme follows your account.
        </p>
        {(form.name || form.accent || true) && (
          <div className="themecreator card pad">
            <h3>{editing ? 'Edit your theme' : 'Make your own'}</h3>
            <div className="airow">
              <label htmlFor="theme-name">Name</label>
              <input id="theme-name" value={editing ? editing.name : form.name} onChange={(e) => (editing ? setEditing({ ...editing, name: e.target.value }) : setForm({ ...form, name: e.target.value }))} placeholder="My theme" />
            </div>
            <div className="airow">
              <label htmlFor="theme-accent">Accent</label>
              <input id="theme-accent" type="color" value={editing ? editing.accent : form.accent} onChange={(e) => (editing ? setEditing({ ...editing, accent: e.target.value }) : setForm({ ...form, accent: e.target.value }))} />
            </div>
            <div className="airow">
              <label htmlFor="theme-base">Base</label>
              <select id="theme-base" value={editing ? editing.base : form.base} onChange={(e) => (editing ? setEditing({ ...editing, base: e.target.value as 'light' | 'dark' }) : setForm({ ...form, base: e.target.value as 'light' | 'dark' }))}>
                <option value="light">Light surfaces</option>
                <option value="dark">Dark surfaces</option>
              </select>
            </div>
            <div className="airow">
              <label aria-hidden="true" />
              <button className="primary" onClick={saveCustom}>
                {editing ? 'Save theme' : 'Create and apply'}
              </button>
            </div>
            {editing && (
              <div className="airow">
                <label aria-hidden="true" />
                <button className="small ghost" onClick={() => setEditing(null)}>Cancel edit</button>
              </div>
            )}
          </div>
        )}
        <div className="themerow">
          {s.customThemes.map((t) => (
            <div key={t.id} className={`themecard${s.theme === t.id ? ' selected' : ''}`}>
              <button className="themecardhit" onClick={() => s.setTheme(t.id as ThemePreference)} aria-label={`Use ${t.name}`}>
                <span className="swatch"><i style={{ background: t.tokens['--bg'] }}></i><i style={{ background: t.tokens['--surface'] }}></i><i style={{ background: t.tokens['--accent'] }}></i></span>
                <strong>{t.name}</strong>
              </button>
              <div className="themecardactions">
                <button className="small ghost" aria-label={`Edit ${t.name}`} title="Edit accent" onClick={() => setEditing({ id: t.id, name: t.name, accent: t.accent, base: t.base as 'light' | 'dark' })}>
                  <Icon name="edit" />
                </button>
                <button className="small ghost" aria-label={`Delete ${t.name}`} title="Delete" onClick={() => s.deleteCustomTheme(t.id)}>
                  <Icon name="x" />
                </button>
              </div>
            </div>
          ))}
          <button className={`themecard${s.theme === 'system' ? ' selected' : ''}`} onClick={() => s.setTheme('system')}>
            <span className="swatch"><i style={{ background: '#f4f5f7' }}></i><i style={{ background: '#181a1f' }}></i><i style={{ background: '#0d7d68' }}></i></span>
            <strong>System</strong>
          </button>
          {THEMES.map((t) => (
            <button key={t.id} className={`themecard${s.theme === t.id ? ' selected' : ''}`} onClick={() => s.setTheme(t.id as ThemePreference)}>
              <span className="swatch"><i style={{ background: t.swatch.bg }}></i><i style={{ background: t.swatch.surface }}></i><i style={{ background: t.swatch.accent }}></i></span>
              <strong>{t.label}</strong>
            </button>
          ))}
        </div>
      </div>

      <div className="card mt-16">
        <h2>Reading &amp; motion</h2>
        <div className="toggle">
          <div><div className="tl" id="set-motion">Reduced motion</div><div className="ts">Disable transitions and animations</div></div>
          <Switch labelledBy="set-motion" on={s.motion} onClick={s.setMotion} />
        </div>
        <div className="toggle">
          <div><div className="tl" id="set-grids">Wide result grids</div><div className="ts">Allow horizontal scroll instead of wrapping cells</div></div>
          <Switch labelledBy="set-grids" on={true} onClick={() => s.toast('Result grids already scroll horizontally.')} />
        </div>
        <div className="toggle">
          <div><div className="tl" id="set-override">Confirm before accepting an override</div><div className="ts">Ask before recording an accepted-by-you answer</div></div>
          <Switch labelledBy="set-override" on={true} onClick={() => s.toast('Override confirmation would be a product setting.')} />
        </div>
      </div>

      <div className="card mt-16">
        <h2>Engine &amp; storage</h2>
        <div className="reportrow"><span>Execution engine</span><span className="mono">{engineLine}</span></div>
        <div className="reportrow"><span>Deadline</span><span className="mono">{deadlineLine}</span></div>
        <div className="reportrow"><span>Isolation</span><span>{isolationLine}</span></div>
        <div className="reportrow"><span>Workbook storage</span><span className="mono">{storageLine}</span></div>
        <div className="mt-14 togglerow">
          <button className="ghost" onClick={s.loadSampleProgress}>Load sample data</button>
          <button className="ghost" onClick={s.exportData}><Icon name="download" />Export my data</button>
          <ImportControl className="ghost" label="Import a file" />
          <button className="ghost" onClick={s.clearProgress}>Clear my data</button>
        </div>
        <p className="ts mt-8">A new workspace starts empty. Sample data is a generated demo — it is never loaded unless you ask for it.</p>
        <p className="ts mt-8">Import merges rather than replaces: a note you already wrote is not overwritten, an attempt that is already recorded is not duplicated, and nothing is deleted. A file this build does not understand is refused before anything is written.</p>
      </div>

      <div className="card mt-16">
        <h2>API surface</h2>
        <p className="mt-8">
          SQL execution, the dataset, course and challenge generation, chapter content and saved progress all arrive through one async service. The adapter below is what the screens are talking to right now — nothing above it is simulated.
        </p>
        <code className="apiurl">{s.api.baseUrl}</code>
        <div className="toggle">
          <div><div className="tl" id="set-latency">Simulate network latency</div><div className="ts">Adds a short round-trip so loading and error states are real</div></div>
          <Switch labelledBy="set-latency" on={s.latency} onClick={() => s.setLatency(!s.latency)} />
        </div>
        <div className="reportrow"><span>Engine behind the API</span><span className="mono">{s.api.config.engine}</span></div>
        <div className="reportrow"><span>Requests this session</span><span className="mono">{s.apiCount}</span></div>
        <div className="reportrow"><span>Last response</span><span className="mono">{last ? `${last.method} ${last.path} · ${last.status} · ${last.ms} ms` : '—'}</span></div>
        <h3 className="mt-16">Recent requests</h3>
        <div className="apilog">
          {s.apiLog.length === 0 ? (
            <div className="empty">No requests yet. Run a query or build a course and they appear here.</div>
          ) : (
            s.apiLog.map((r, i) => {
              const ok = r.status < 400;
              return (
                <div className="apireq" key={i}>
                  <span className="verb">{r.method}</span>
                  <span>{r.path}</span>
                  <span className="ms">{r.ms} ms</span>
                  <span className={`stat ${ok ? 'ok' : 'err'}`}>{r.status}</span>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
