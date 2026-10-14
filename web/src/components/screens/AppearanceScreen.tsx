import React from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import ImportControl from '../ui/ImportControl';


function Switch({ on, onClick }: { on: boolean; onClick: () => void }) {
  return <div className="switch" role="switch" aria-checked={on} tabIndex={0} onClick={onClick} onKeyDown={(e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); onClick(); } }}></div>;
}

export default function AppearanceScreen() {
  const s = useStore();
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
        <p className="mt-8">Choose the palette. System follows your OS setting.</p>
        <div className="themerow">
          <button className={`themecard${s.theme === 'light' ? ' selected' : ''}`} onClick={() => s.setTheme('light')}>
            <span className="swatch"><i style={{ background: '#f4f5f7' }}></i><i style={{ background: '#ffffff' }}></i><i style={{ background: '#0d7d68' }}></i></span>
            <strong>Light · Ink &amp; Pine</strong>
          </button>
          <button className={`themecard${s.theme === 'dark' ? ' selected' : ''}`} onClick={() => s.setTheme('dark')}>
            <span className="swatch"><i style={{ background: '#121316' }}></i><i style={{ background: '#181a1f' }}></i><i style={{ background: '#34d3a6' }}></i></span>
            <strong>Dark · Pine on Slate</strong>
          </button>
          <button className={`themecard${s.theme === 'system' ? ' selected' : ''}`} onClick={() => s.setTheme('system')}>
            <span className="swatch"><i style={{ background: '#f4f5f7' }}></i><i style={{ background: '#181a1f' }}></i><i style={{ background: '#0d7d68' }}></i></span>
            <strong>System</strong>
          </button>
        </div>
      </div>

      <div className="card mt-16">
        <h2>Reading &amp; motion</h2>
        <div className="toggle">
          <div><div className="tl">Reduced motion</div><div className="ts">Disable transitions and animations</div></div>
          <Switch on={s.motion} onClick={s.setMotion} />
        </div>
        <div className="toggle">
          <div><div className="tl">Wide result grids</div><div className="ts">Allow horizontal scroll instead of wrapping cells</div></div>
          <Switch on={true} onClick={() => s.toast('Result grids already scroll horizontally.')} />
        </div>
        <div className="toggle">
          <div><div className="tl">Confirm before accepting an override</div><div className="ts">Ask before recording an accepted-by-you answer</div></div>
          <Switch on={true} onClick={() => s.toast('Override confirmation would be a product setting.')} />
        </div>
      </div>

      <div className="card mt-16">
        <h2>Engine &amp; storage</h2>
        <div className="reportrow"><span>Execution engine</span><span className="mono">{engineLine}</span></div>
        <div className="reportrow"><span>Deadline</span><span className="mono">{deadlineLine}</span></div>
        <div className="reportrow"><span>Isolation</span><span>rolled-back transaction per query</span></div>
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
          <div><div className="tl">Simulate network latency</div><div className="ts">Adds a short round-trip so loading and error states are real</div></div>
          <Switch on={s.latency} onClick={() => s.setLatency(!s.latency)} />
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
