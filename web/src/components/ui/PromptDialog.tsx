import { useEffect, useRef, useState } from 'react';
import Icon from './Icon';
import { useDrawerFocus } from './useDrawerFocus';
import { useStore } from '../../state/store';
import type { PromptRequest } from '../../types';

const INPUT_ID = 'prompt-dialog-input';

function PromptForm({ req, onCancel }: { req: PromptRequest; onCancel: () => void }) {
  const [value, setValue] = useState(req.defaultValue ?? '');
  const inputRef = useRef<HTMLInputElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  /* The prompt covers the page, so it shares the shell's focus contract: Tab
     cycles inside it instead of walking into the shell behind. It focuses its
     own input rather than the first control (the Close button), because the
     learner opened it to type. Escape and the backdrop are the exits. */
  useDrawerFocus(true, formRef, inputRef);

  /* The dialog is mounted per request (the provider renders nothing between
     prompts), so `value` already starts from this request's default. Focusing on
     mount is all that is left; there is no state to reset when a prop changes. */
  useEffect(() => {
    const input = inputRef.current;
    if (input) {
      input.focus();
      input.select();
    }
  }, []);

  const trimmed = value.trim();
  const blocked = req.required !== false && trimmed === '';

  function submit() {
    if (blocked) return;
    req.onSubmit(trimmed);
    onCancel();
  }

  return (
    /* Deliberate deferral: the native `<dialog>` element would give the modal
       semantics for free, but this app renders four modals from one pattern and
       migrating one of them is a redesign, not a fix. */
    <div
      className="modalback open"
      onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
      /* The backdrop click is a pointer convenience. Escape and the Close button
         are the keyboard path, and the form below traps Tab. */
    >
      <form
        ref={formRef}
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
