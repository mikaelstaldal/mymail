/** Activate the same visible, enabled buttons used by pointer users. */
export function handleActionShortcut(event: KeyboardEvent): void {
  if (event.defaultPrevented || event.repeat || event.isComposing
    || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
  const key = event.key.toLowerCase();
  if (!['c', 'r', 'a', 'f', 'd', 's', 'm'].includes(key)) return;
  const target = event.target;
  if (target instanceof HTMLElement && (target.isContentEditable
    || target.closest('input, textarea, select, [contenteditable], [role="textbox"]'))) return;
  if (document.querySelector('[aria-modal="true"]')) return;
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>(`button[data-shortcut="${key}"]`))
    .find(button => !button.disabled && button.getClientRects().length > 0);
  if (!button) return;
  event.preventDefault();
  button.click();
}
