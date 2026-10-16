import { useEffect, useRef, useState } from 'react';
import Icon from './Icon';
import { useStore } from '../../state/store';
import type { PromptRequest } from '../../types';

const INPUT_ID = 'prompt-dialog-input';

function PromptForm({ req, onCancel }: { req: PromptRequest; onCancel: () => void }) {
  const [value, setValue] = useState(req.defaultValue ?? '');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setValue(req.defaultValue ?? '');
    const input = inputRef.current;
    if (input) {
      input.focus();
      input.select();
    }
  }, [req]);

  const trimmed = value.trim();
  const blocked = req.required !== false && trimmed === '';

  function submit() {
    if (blocked) return;
    req.onSubmit(trimmed);
    onCancel();
  }

  return (
    <div
      className="modalback open"
      onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
    >
      <form
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={req.title}
        onSubmit={(e) => { e.preventDefault(); submit(); }}
        onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onCancel(); } }}
      >
        <div className="modalhead">
          <div>
            <div className="eyebrow">{req.label}</div>
            <h2>{req.title}</h2>
          </div>
          <button type="button" className="small" aria-label="Close" onClick={onCancel}><Icon name="x" /></button>
        </div>
        <div className="field">
          <label htmlFor={INPUT_ID}>{req.label}</label>
          <input
            id={INPUT_ID}
            ref={inputRef}
            value={value}
            placeholder={req.placeholder}
            onChange={(e) => setValue(e.target.value)}
          />
        </div>
        <div className="modalfoot">
          <button type="button" className="ghost" onClick={onCancel}>Cancel</button>
          <button type="submit" className="primary" disabled={blocked}>{req.submitLabel || 'Save'}</button>
        </div>
      </form>
    </div>
  );
}

export default function PromptDialog() {
  const { promptRequest, closePrompt } = useStore();
  if (!promptRequest) return null;
  return <PromptForm req={promptRequest} onCancel={closePrompt} />;
}
