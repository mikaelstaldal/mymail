// The confirmation dialogs that replaced `window.confirm`. Same shape as
// util/toast.ts: a module-level store the view subscribes to, so a handler deep
// in a view can ask a question without the answer having to be threaded back
// through props.
//
// `window.confirm` was synchronous and blocking; this is neither, so a caller
// awaits the answer. Everything a native confirm could not do is why it went:
// the buttons say what they do ("Keep" / "Delete") instead of "Cancel" / "OK",
// the dialog is styled with the rest of the app, and it does not freeze the
// page or announce the origin above the question.

export interface ConfirmOptions {
  // Short heading, e.g. "Delete message". No trailing question mark.
  title: string;
  // The question itself, in full.
  body: string;
  // The button that goes ahead, named after the action ("Delete", "Send") —
  // never "OK". This label is the last thing read before committing.
  confirmLabel: string;
  // The button that does nothing: "Keep" when the action deletes something,
  // "Cancel" otherwise.
  cancelLabel: string;
  // Colours the confirm button as destructive.
  destructive?: boolean;
}

export interface ConfirmRequest extends ConfirmOptions {
  id: number;
  resolve: (confirmed: boolean) => void;
}

type Listener = (request: ConfirmRequest | null) => void;

// At most one question is outstanding: the dialog is modal, so nothing else can
// be clicked while it is up.
let current: ConfirmRequest | null = null;
let nextId = 0;
const listeners = new Set<Listener>();

function notify(): void {
  for (const l of listeners) l(current);
}

export function subscribe(listener: Listener): () => void {
  listeners.add(listener);
  listener(current);
  return () => { listeners.delete(listener); };
}

export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  // A second question arriving while one is open (a keyboard shortcut firing
  // behind the overlay, say) would strand the first promise unresolved and its
  // caller's `await` with it — so answer it "no" rather than dropping it.
  current?.resolve(false);
  return new Promise<boolean>(resolve => {
    current = { ...options, id: nextId++, resolve };
    notify();
  });
}

// Answer the open question. The id guards against a stale click resolving a
// question that has already been superseded.
export function answerConfirm(id: number, confirmed: boolean): void {
  if (current === null || current.id !== id) return;
  const { resolve } = current;
  current = null;
  notify();
  resolve(confirmed);
}
