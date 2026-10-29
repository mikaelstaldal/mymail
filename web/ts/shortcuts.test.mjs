// Exercises action dispatch and typing/modal guards against real DOM buttons.
// Button placement and rendered underlines remain the components' responsibility.
import test from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from './third_party/test/jsdom.js';
const { window } = new JSDOM('<!doctype html><body></body>');
Object.assign(globalThis, { document: window.document, HTMLElement: window.HTMLElement });
const { handleActionShortcut } = await import('../static/util/shortcuts.js');

test('action shortcuts use available buttons and preserve editing and modal ownership', () => {
  for (const key of ['c', 'r', 'a', 'f', 'd', 's', 'm']) {
    document.body.innerHTML = `<button data-shortcut="${key}">Action</button><input><textarea></textarea>
      <select></select><div contenteditable="true"><span>Editor</span></div><div role="textbox"></div>`;
    const button = document.querySelector('button');
    button.getClientRects = () => [{}];
    let clicks = 0;
    button.onclick = () => clicks++;
    const press = (target = document.body, options = {}) => {
      const event = new window.KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...options });
      target.addEventListener('keydown', handleActionShortcut, { once: true });
      target.dispatchEvent(event);
      return event.defaultPrevented;
    };
    assert.equal(press(), true);
    assert.equal(clicks, 1);
    for (const target of document.querySelectorAll('input, textarea, select, span, [role="textbox"]')) {
      assert.equal(press(target), false);
    }
    for (const flag of ['repeat', 'isComposing', 'altKey', 'ctrlKey', 'metaKey', 'shiftKey']) {
      assert.equal(press(document.body, { [flag]: true }), false);
    }
    const modal = document.createElement('div');
    modal.setAttribute('aria-modal', 'true');
    document.body.append(modal);
    assert.equal(press(), false);
    modal.remove();
    button.disabled = true;
    assert.equal(press(), false);
    button.disabled = false;
    button.getClientRects = () => [];
    assert.equal(press(), false);
    button.remove();
    assert.equal(press(), false);
    assert.equal(clicks, 1);
  }
});
