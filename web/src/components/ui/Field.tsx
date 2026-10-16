import { useId } from 'react';
import type { ReactNode } from 'react';

/*
 * A label and its control, associated.
 *
 * The id comes from `useId` rather than from a written string. Four modals are
 * mounted at the same time, so two of them would claim the same hand-written id
 * and a screen reader would read one field's name for another's control.
 *
 * The child is a function of the id. That is the only way to give the same
 * value to `htmlFor` and to the control without asking every call site for a
 * second prop.
 */
export default function Field({
  label,
  children,
}: {
  label: string;
  children: (id: string) => ReactNode;
}) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {children(id)}
    </div>
  );
}
