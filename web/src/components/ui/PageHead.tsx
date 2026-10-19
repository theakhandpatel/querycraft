import type React from 'react';

/*
 * The heading block every screen opens with: an eyebrow, the title, one
 * sentence of context, and at most one primary action on the right.
 *
 * It was repeated as the same seven-node subtree on Home and Library. The
 * layout lives in `.pagehead`, so the markup lives here once.
 */
export default function PageHead({
  eyebrow,
  title,
  children,
  action,
}: {
  eyebrow: string;
  title: string;
  children: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="pagehead">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
        {children}
      </div>
      {action}
    </div>
  );
}
