import { useState, useEffect, useRef } from 'preact/hooks';
import { subscribe, answerConfirm, type ConfirmRequest } from '../util/confirm.js';

// Renders whatever question `confirmDialog()` currently has outstanding, or
// nothing. Mounted once, next to <Toast />.
export function ConfirmDialog() {
  const [request, setRequest] = useState<ConfirmRequest | null>(null);

  useEffect(() => subscribe(setRequest), []);

  if (request === null) return null;
  // Keyed by id so a second question gets a fresh dialog — focus and the key
  // handler are set up per question, not per mount.
  return <Dialog key={request.id} request={request} />;
}

function Dialog({ request }: { request: ConfirmRequest }) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    // Whatever was focused when the question was asked gets it back once the
    // dialog closes — unless the answer navigated away, in which case the old
    // element is detached and focusing it does nothing.
    const previous = document.activeElement as HTMLElement | null;
    // A destructive question opens with the declining button focused, so a
    // reflexive Enter keeps the thing rather than deleting it — the point of
    // saying "Keep" and "Delete" is that the choice is read, not defaulted
    // into. Everything else opens on the confirming button.
    (request.destructive ? cancelRef : confirmRef).current?.focus();

    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault();
        answerConfirm(request.id, false);
        return;
      }
      // aria-modal="true" promises focus stays inside, and the dialog holds
      // exactly two focusable elements — so moving to the other one is the
      // whole trap, in both directions.
      if (e.key === 'Tab') {
        e.preventDefault();
        const onConfirm = document.activeElement === confirmRef.current;
        (onConfirm ? cancelRef : confirmRef).current?.focus();
      }
    }
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('keydown', handleKey);
      previous?.focus();
    };
  }, [request.id, request.destructive]);

  return (
    <div class="dialog-overlay" onClick={() => answerConfirm(request.id, false)}>
      <div
        class="dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-body"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="confirm-title" class="dialog-title">{request.title}</h2>
        <div id="confirm-body" class="dialog-body"><p>{request.body}</p></div>
        <div class="dialog-actions">
          <button
            ref={cancelRef}
            type="button"
            class="btn btn-ghost"
            onClick={() => answerConfirm(request.id, false)}
          >
            {request.cancelLabel}
          </button>
          <button
            ref={confirmRef}
            type="button"
            class={`btn ${request.destructive ? 'btn-danger' : 'btn-primary'}`}
            onClick={() => answerConfirm(request.id, true)}
          >
            {request.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
