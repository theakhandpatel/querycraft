import { useEffect, useRef, useState } from 'react';
import type { Dialect } from '../../types';
import type { SqlSchema } from '../../lib/sqlSchema';
import type { SqlEditorHandle, SqlEditorOptions } from './sqlEditorRuntime';

/*
 * The SQL editor boundary.
 *
 * The CodeMirror runtime is imported only when a cell mounts, so the landing,
 * the shell and the first paint do not carry the editor. A plain textarea is
 * rendered underneath until that import resolves. It is the same controlled
 * value and the same Mod-Enter action, so a learner who types immediately does
 * not lose a keystroke and no layout shifts when the real editor arrives.
 *
 * Everything below this file is the editor's own lifecycle. The cell still owns
 * the query, the verdict and the run action.
 */

interface SqlEditorProps {
  value: string;
  dialect: Dialect;
  schema: SqlSchema | null;
  ariaLabel: string;
  describedBy?: string;
  onChange: (value: string) => void;
  onRun: () => void;
  /** Called on the first focus or keystroke. The cell uses it to load the
      schema for completion, not to run anything. */
  onFirstInteraction?: () => void;
}

export default function SqlEditor({
  value,
  dialect,
  schema,
  ariaLabel,
  describedBy,
  onChange,
  onRun,
  onFirstInteraction,
}: SqlEditorProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const runtimeRef = useRef<SqlEditorHandle | null>(null);
  const [ready, setReady] = useState(false);

  /* The mount effect runs once. These refs let the editor call the latest
     callbacks without being torn down on every render. */
  const changeRef = useRef(onChange);
  const runRef = useRef(onRun);
  changeRef.current = onChange;
  runRef.current = onRun;

  const latest = useRef<SqlEditorOptions>({
    value,
    dialect,
    schema,
    ariaLabel,
    describedBy,
    onChange: (next) => changeRef.current(next),
    onRun: () => runRef.current(),
  });
  latest.current = {
    value,
    dialect,
    schema,
    ariaLabel,
    describedBy,
    onChange: (next) => changeRef.current(next),
    onRun: () => runRef.current(),
  };

  useEffect(() => {
    let alive = true;
    let created: SqlEditorHandle | null = null;
    import('./sqlEditorRuntime')
      .then(({ createSqlEditor }) => {
        if (!alive || !hostRef.current) return;
        created = createSqlEditor(hostRef.current, latest.current);
        runtimeRef.current = created;
        setReady(true);
      })
      .catch(() => {
        /* The textarea below is the complete fallback. A failed chunk load
           must not cost the learner the exercise. */
        console.warn('[querycraft] the rich SQL editor could not load; using the plain editor.');
      });
    return () => {
      alive = false;
      runtimeRef.current = null;
      created?.destroy();
    };
  }, []);

  useEffect(() => {
    runtimeRef.current?.setValue(value);
  }, [value]);

  useEffect(() => {
    runtimeRef.current?.setConfig({ dialect, schema, ariaLabel, describedBy });
  }, [dialect, schema, ariaLabel, describedBy]);

  function interaction() {
    onFirstInteraction?.();
  }

  return (
    <div ref={hostRef} className="sql-editor-host" onFocus={interaction} onKeyDown={interaction}>
      {!ready && (
        <textarea
          className="editor sql-editor-fallback"
          spellCheck="false"
          aria-label={ariaLabel}
          aria-describedby={describedBy}
          value={value}
          onChange={(event) => {
            interaction();
            onChange(event.target.value);
          }}
          onFocus={interaction}
          onKeyDown={(event) => {
            if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
              event.preventDefault();
              onRun();
            }
          }}
        />
      )}
    </div>
  );
}
