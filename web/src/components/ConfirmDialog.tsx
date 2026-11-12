import { useEffect, useId, useRef, type ReactNode } from 'react';

interface Props {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel: string;
  dismissLabel: string;
  busy?: boolean;
  error?: string;
  onConfirm: () => void;
  onDismiss: () => void;
}

/**
 * A modal confirmation built on the native <dialog>: the browser traps focus inside it, closes it
 * on Escape, and returns focus to the button that opened it. The safe choice comes first, so it
 * receives focus when the dialog opens.
 */
export function ConfirmDialog({ open, title, children, confirmLabel, dismissLabel, busy, error, onConfirm, onDismiss }: Props) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);

  return (
    <dialog ref={ref} className="dialog" aria-labelledby={`${id}-title`} aria-describedby={`${id}-body`} onClose={onDismiss}>
      <h2 id={`${id}-title`}>{title}</h2>
      <div id={`${id}-body`}>{children}</div>
      {error && (
        <p className="notice error" role="alert">
          {error}
        </p>
      )}
      <div className="actions">
        <button type="button" onClick={onDismiss}>
          {dismissLabel}
        </button>
        <button type="button" className="danger" aria-disabled={busy || undefined} onClick={() => !busy && onConfirm()}>
          {busy ? 'Working…' : confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
