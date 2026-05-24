import { useState, useEffect } from 'preact/hooks';
import { subscribe, dismissToast, type ToastItem } from '../util/toast.js';

export function Toast() {
  const [items, setItems] = useState<ToastItem[]>([]);

  useEffect(() => subscribe(setItems), []);

  if (items.length === 0) return null;

  return (
    <div class="toast-container" aria-live="polite" aria-atomic="false">
      {items.map(item => (
        <div key={item.id} class={`toast-item${item.persistent ? ' toast-persistent' : ''}`}>
          <span class="toast-message">{item.message}</span>
          <div class="toast-btns">
            {item.onRetry && (
              <button
                class="toast-retry-btn"
                onClick={() => { dismissToast(item.id); item.onRetry!(); }}
              >
                Retry
              </button>
            )}
            <button
              class="toast-close-btn"
              onClick={() => dismissToast(item.id)}
              aria-label="Dismiss"
            >
              ✕
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
