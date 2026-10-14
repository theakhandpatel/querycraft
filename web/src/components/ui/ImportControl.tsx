import React, { useRef, useState } from 'react';
import Icon from './Icon';
import { useStore } from '../../state/store';

/**
 * The import half of the export/import pair (design 8.3).
 *
 * The control owns exactly one thing the store cannot: reading the chosen file.
 * The decision of whether the document is acceptable, and what merging it does,
 * lives in `lib/importBundle.ts`, so a bad file is refused before a byte of it
 * reaches the workbook and the merge policy is the same here, in the workbook
 * and in Appearance.
 */
export default function ImportControl({
  className = 'small ghost',
  label = 'Import',
  title = 'Read a versioned JSON export and merge it into your workbook',
}: {
  className?: string;
  label?: string;
  title?: string;
}) {
  const s = useStore();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);

  async function onChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files && e.target.files[0];
    /* Clear first, so picking the same file twice in a row still imports. */
    e.target.value = '';
    if (!file) return;
    setBusy(true);
    try {
      s.importData(await file.text());
    } catch (err) {
      s.toast(`Could not read ${file.name} — ${err instanceof Error ? err.message : String(err)}.`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <button className={className} onClick={() => input.current?.click()} disabled={busy} title={title}>
        <Icon name="upload" />
        {busy ? 'Reading…' : label}
      </button>
      <input
        ref={input}
        className="visually-hidden"
        type="file"
        accept="application/json,.json"
        onChange={onChange}
        aria-label="Choose a Querycraft export file to import"
      />
    </>
  );
}
