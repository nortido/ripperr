import { cancelClose, closeAnimated } from './components.js';
import { ICONS, hydrateIcons, icon } from './icons.js';
import { overlayBridge } from './state.js';
import { html } from './util.js';

export let openCustomSelect = null;

export function closeOpenCustomSelect(force) {
  if (!openCustomSelect) return;
  startSelectClose(openCustomSelect, force);
}

function finishSelectClose(wrap) {
  cancelClose(wrap);
  wrap.classList.remove('open', 'open-up');
  const panel = wrap.querySelector('.custom-select-panel');
  // hidden ставим после анимации выхода: ей нужна нарисованная панель
  if (panel) panel.hidden = true;
}

function startSelectClose(wrap, force) {
  const trigger = wrap.querySelector('.custom-select-trigger');
  const panel = wrap.querySelector('.custom-select-panel');
  const wasOpen = wrap.classList.contains('open');
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  if (openCustomSelect === wrap) openCustomSelect = null;
  wrap.classList.remove('open');
  if (force || !wasOpen || !panel || panel.hidden) {
    finishSelectClose(wrap);
    return;
  }
  closeAnimated(wrap, function () { finishSelectClose(wrap); });
}
overlayBridge.getOpenCustomSelect = function () { return openCustomSelect; };
overlayBridge.closeCustomSelect = closeOpenCustomSelect;

export function initCustomSelect(selectEl) {
  if (!selectEl || selectEl.dataset.customSelectInit) return;
  selectEl.dataset.customSelectInit = '1';

  const wrap = document.createElement('div');
  wrap.className = 'custom-select';
  selectEl.parentNode.insertBefore(wrap, selectEl);
  wrap.appendChild(selectEl);
  selectEl.classList.add('custom-select-native');

  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.className = 'custom-select-trigger';
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  if (selectEl.id) trigger.id = selectEl.id + '-trigger';

  const labelEl = selectEl.id
    ? document.querySelector('label[for="' + CSS.escape(selectEl.id) + '"]')
    : null;
  if (labelEl && trigger.id) labelEl.setAttribute('for', trigger.id);

  const triggerLabel = document.createElement('span');
  triggerLabel.className = 'custom-select-value';
  const chevron = document.createElement('span');
  chevron.className = 'custom-select-chevron';
  chevron.innerHTML = ICONS.chevron;
  trigger.appendChild(triggerLabel);
  trigger.appendChild(chevron);

  const panel = document.createElement('div');
  panel.className = 'custom-select-panel';
  panel.setAttribute('role', 'listbox');
  panel.hidden = true;

  wrap.appendChild(trigger);
  wrap.appendChild(panel);

  let highlightIdx = selectEl.selectedIndex;

  function renderOptions() {
    panel.innerHTML = '';
    const curVal = selectEl.value;
    Array.from(selectEl.options).forEach(function (opt, idx) {
      const item = document.createElement('div');
      item.className = 'custom-select-option' + (opt.value === curVal ? ' is-selected' : '');
      item.setAttribute('role', 'option');
      item.setAttribute('aria-selected', opt.value === curVal ? 'true' : 'false');
      item.dataset.index = String(idx);
      if (opt.disabled) item.setAttribute('aria-disabled', 'true');
      if (opt.value === curVal) {
        item.innerHTML = html`<span>${opt.textContent}</span><span class="custom-select-check">${icon('check')}</span>`;
      } else {
        item.textContent = opt.textContent;
      }
      if (!opt.disabled) {
        item.addEventListener('click', function (e) {
          e.stopPropagation();
          choose(idx);
        });
      }
      panel.appendChild(item);
    });
    hydrateIcons(panel);
  }

  function syncTrigger() {
    const opt = selectEl.options[selectEl.selectedIndex];
    triggerLabel.textContent = opt ? opt.textContent : '';
    trigger.disabled = selectEl.disabled;
    renderOptions();
  }

  function choose(idx) {
    if (idx < 0 || idx >= selectEl.options.length || selectEl.options[idx].disabled) return;
    selectEl.selectedIndex = idx;
    selectEl.dispatchEvent(new Event('change', { bubbles: true }));
    syncTrigger();
    closePanel();
  }

  function closePanel() {
    startSelectClose(wrap);
  }

  function positionPanel() {
    wrap.classList.remove('open-up');
    panel.style.cssText = '';
    const rect = trigger.getBoundingClientRect();
    const panelH = Math.min(panel.scrollHeight || 320, 320);
    const spaceBelow = window.innerHeight - rect.bottom;
    const spaceAbove = rect.top;
    const up = spaceBelow < panelH + 8 && spaceAbove > spaceBelow;
    if (up) wrap.classList.add('open-up');
    if (!wrap.closest('.modal-body')) return;
    // тело диалога прокручивается и обрезало список, поэтому в диалоге панель
    // фиксированная по координатам кнопки и не выше свободного места
    const room = Math.max(120, (up ? spaceAbove : spaceBelow) - 12);
    // на мобильном окно под zoom страницы: координаты экрана делим на масштаб, иначе их умножит zoom
    const z = document.documentElement.classList.contains('mobile-layout')
      ? parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--page-scale')) || 1
      : 1;
    panel.style.position = 'fixed';
    panel.style.left = rect.left / z + 'px';
    panel.style.right = 'auto';
    panel.style.width = rect.width / z + 'px';
    panel.style.maxHeight = Math.min(320, room / z) + 'px';
    if (up) {
      panel.style.top = 'auto';
      panel.style.bottom = (window.innerHeight - rect.top + 4) / z + 'px';
    } else {
      panel.style.top = (rect.bottom + 4) / z + 'px';
      panel.style.bottom = 'auto';
    }
  }

  function followScroll(e) {
    if (e && e.target === panel) return;
    if (!wrap.classList.contains('open')) {
      window.removeEventListener('scroll', followScroll, true);
      window.removeEventListener('resize', followScroll);
      return;
    }
    positionPanel();
  }

  function scrollToIndex(idx) {
    const el = panel.querySelector('[data-index="' + idx + '"]');
    if (el) el.scrollIntoView({ block: 'nearest' });
  }

  function highlightOption() {
    panel.querySelectorAll('.custom-select-option').forEach(function (el) {
      el.classList.toggle('is-highlighted', parseInt(el.dataset.index, 10) === highlightIdx);
    });
  }

  function openPanel() {
    closeOpenCustomSelect();
    cancelClose(wrap);
    wrap.classList.add('open');
    trigger.setAttribute('aria-expanded', 'true');
    panel.hidden = false;
    openCustomSelect = wrap;
    highlightIdx = selectEl.selectedIndex;
    renderOptions();
    positionPanel();
    if (wrap.closest('.modal-body')) {
      window.addEventListener('scroll', followScroll, true);
      window.addEventListener('resize', followScroll);
    }
    scrollToIndex(highlightIdx);
    highlightOption();
  }

  trigger.addEventListener('click', function (e) {
    e.stopPropagation();
    if (wrap.classList.contains('open')) closePanel();
    else openPanel();
  });

  trigger.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!wrap.classList.contains('open')) openPanel();
      else choose(highlightIdx);
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!wrap.classList.contains('open')) {
        openPanel();
        return;
      }
      const step = e.key === 'ArrowDown' ? 1 : -1;
      let next = highlightIdx + step;
      while (next >= 0 && next < selectEl.options.length && selectEl.options[next].disabled) {
        next += step;
      }
      if (next >= 0 && next < selectEl.options.length) {
        highlightIdx = next;
        highlightOption();
        scrollToIndex(highlightIdx);
      }
    }
  });

  selectEl.addEventListener('change', syncTrigger);
  const valueDesc = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');
  if (valueDesc && valueDesc.set) {
    Object.defineProperty(selectEl, 'value', {
      configurable: true,
      get: function () { return valueDesc.get.call(this); },
      set: function (v) {
        valueDesc.set.call(this, v);
        syncTrigger();
      },
    });
  }
  syncTrigger();
}

export function initCustomSelects(root) {
  (root || document).querySelectorAll('select.select:not([data-custom-select-init])').forEach(initCustomSelect);
}
