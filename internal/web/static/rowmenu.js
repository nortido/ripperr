import { ICONS } from './icons.js';

let current = null;

export function closeRowMenu() {
  const cur = current;
  if (!cur) return;
  current = null;
  window.removeEventListener('pointerdown', cur.onPointer, true);
  window.removeEventListener('keydown', cur.onKey, true);
  window.removeEventListener('scroll', cur.onDismiss, true);
  window.removeEventListener('resize', cur.onDismiss);
  cur.menu.remove();
  cur.anchor.setAttribute('aria-expanded', 'false');
  if (cur.anchor.isConnected && !cur.anchor.disabled) cur.anchor.focus({ preventScroll: true });
}

export function openRowMenu(anchor, items) {
  // повторный клик по той же кнопке закрывает меню
  if (current && current.anchor === anchor) {
    closeRowMenu();
    return;
  }
  closeRowMenu();

  const menu = document.createElement('div');
  menu.className = 'row-menu';
  menu.setAttribute('role', 'menu');
  const buttons = items.map(function (item) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'row-menu-item' + (item.danger ? ' row-menu-item-danger' : '');
    b.setAttribute('role', 'menuitem');
    if (item.title) b.title = item.title;
    if (item.disabled) b.disabled = true;
    if (item.icon && ICONS[item.icon]) {
      const ic = document.createElement('span');
      ic.className = 'row-menu-icon';
      ic.innerHTML = ICONS[item.icon];
      b.appendChild(ic);
    }
    const label = document.createElement('span');
    label.textContent = item.label;
    b.appendChild(label);
    b.addEventListener('click', function () {
      closeRowMenu();
      item.onSelect();
    });
    menu.appendChild(b);
    return b;
  });

  function onDismiss() { closeRowMenu(); }
  function onPointer(e) {
    if (menu.contains(e.target) || anchor.contains(e.target)) return;
    closeRowMenu();
  }
  function onKey(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      closeRowMenu();
      return;
    }
    if (e.key === 'Tab') {
      closeRowMenu();
      return;
    }
    const enabled = buttons.filter(function (b) { return !b.disabled; });
    if (!enabled.length) return;
    const at = enabled.indexOf(document.activeElement);
    let next = -1;
    if (e.key === 'ArrowDown') next = at < 0 ? 0 : (at + 1) % enabled.length;
    else if (e.key === 'ArrowUp') next = at < 0 ? enabled.length - 1 : (at - 1 + enabled.length) % enabled.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = enabled.length - 1;
    if (next < 0) return;
    e.preventDefault();
    enabled[next].focus();
  }

  menu.style.visibility = 'hidden';
  document.body.appendChild(menu);
  // позиция fixed: меню под кнопкой, по ее правому краю, внутри окна; не влезло вниз, открываем вверх
  const r = anchor.getBoundingClientRect();
  const w = menu.offsetWidth;
  const h = menu.offsetHeight;
  const vw = document.documentElement.clientWidth;
  const vh = document.documentElement.clientHeight;
  const left = Math.max(8, Math.min(r.right - w, vw - w - 8));
  let top = r.bottom + 4;
  if (top + h > vh - 8) top = Math.max(8, r.top - h - 4);
  menu.style.left = left + 'px';
  menu.style.top = top + 'px';
  menu.style.visibility = '';

  anchor.setAttribute('aria-haspopup', 'menu');
  anchor.setAttribute('aria-expanded', 'true');
  current = { menu: menu, anchor: anchor, onPointer: onPointer, onKey: onKey, onDismiss: onDismiss };
  window.addEventListener('pointerdown', onPointer, true);
  window.addEventListener('keydown', onKey, true);
  window.addEventListener('scroll', onDismiss, true);
  window.addEventListener('resize', onDismiss);
  const first = buttons.find(function (b) { return !b.disabled; });
  if (first) first.focus({ preventScroll: true });
}
