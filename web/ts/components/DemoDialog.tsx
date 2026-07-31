import { useEffect, useRef } from 'preact/hooks';

// Where the "already seen" flag lives. localStorage rather than the demo's
// IndexedDB store on purpose: this is a property of the browser the demo is
// being read in, not of the mailbox, so clearing the mailbox should not bring
// the notice back, and the page can read it synchronously as it renders.
const STORAGE_KEY = 'mymail-demo-notice-seen';

// Whether the demo notice has already been dismissed in this browser. A storage
// failure (private mode, blocked storage) reports "not seen", so the notice is
// shown again rather than swallowed.
export function demoNoticeSeen(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

export function markDemoNoticeSeen(): void {
  try {
    localStorage.setItem(STORAGE_KEY, 'true');
  } catch {
    // Storage is unavailable; the notice will simply appear again next time.
  }
}

interface Props {
  // Dismiss the notice. The caller records that it has been seen.
  onClose: () => void;
}

// Shown once, on the first visit to a demo build, so nobody writes anything
// they care about into a mailbox that lives only in their browser. Dismissed
// with the button, Escape, or a click outside.
export function DemoDialog({ onClose }: Props) {
  const okRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    okRef.current?.focus();
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onClose();
        return;
      }
      // aria-modal="true" promises focus stays inside, and the dialog holds
      // exactly one focusable element — so keeping Tab on it is the whole trap.
      if (e.key === 'Tab') {
        e.preventDefault();
        okRef.current?.focus();
      }
    }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [onClose]);

  return (
    <div class="demo-overlay" onClick={onClose}>
      <div
        class="demo-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="demo-title"
        aria-describedby="demo-body"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="demo-title" class="demo-title">This is a demo</h2>
        <div id="demo-body" class="demo-body">
          <p>
            There is no server behind this MyMail. Everything you see is running in
            your browser, and every message, contact, and setting you create is
            stored there too — nothing is sent anywhere.
          </p>
          <p>
            Mail you send does not leave this page. It lands in Sent, and a made-up
            reply from the recipient turns up in the Inbox a few seconds later, so
            you can see what a conversation looks like.
          </p>
          <p>
            Nothing here is really saved: clearing this site&apos;s data, or opening
            the demo in another browser or a private window, starts over from the
            sample messages. Please don&apos;t keep anything here that you would miss.
          </p>
        </div>
        <div class="demo-actions">
          <button ref={okRef} type="button" class="primary" onClick={onClose}>OK</button>
        </div>
      </div>
    </div>
  );
}
