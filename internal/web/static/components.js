import { closeOpenHistFilterSelect, openHistFilterSelect, positionHistFilterPanel, renderHistFilterMain } from './histfilter.js';
import { ICONS } from './icons.js';
import { closeOpenNameFilter, openNameFilterWrap } from './namefilter.js';
import { closeOpenCustomSelect, initCustomSelects, openCustomSelect } from './select.js';
import { PAGE_SIZE_OPTIONS, formatRunSlot, html, savePageSize } from './util.js';

export let activeErrorPopover = null;
export let activeSlotsPopover = null;
export let slotsPopoverLeaveTimer = null;

export function bindErrorBadges(root) {
  root.querySelectorAll('.status-error-badge').forEach(function (badge) {
    badge.addEventListener('click', function (e) {
      e.stopPropagation();
      const err = badge.getAttribute('data-error') || '';
      closePopovers();
      const pop = document.createElement('div');
      pop.className = 'error-popover';
      pop.textContent = err;
      document.body.appendChild(pop);
      const rect = badge.getBoundingClientRect();
      pop.style.top = (rect.bottom + window.scrollY + 4) + 'px';
      pop.style.left = Math.min(rect.left + window.scrollX, window.innerWidth - pop.offsetWidth - 8) + 'px';
      activeErrorPopover = pop;
    });
  });
}

export function showSlotsPopover(card, slots) {
  if (slotsPopoverLeaveTimer) {
    clearTimeout(slotsPopoverLeaveTimer);
    slotsPopoverLeaveTimer = null;
  }
  closePopovers();
  const pop = document.createElement('div');
  pop.className = 'slots-popover';
  slots.forEach(function (s) {
    const item = document.createElement('div');
    item.className = 'slots-popover-item';
    item.textContent = formatRunSlot(s);
    pop.appendChild(item);
  });
  document.body.appendChild(pop);
  const rect = card.getBoundingClientRect();
  pop.style.top = (rect.bottom + window.scrollY + 6) + 'px';
  pop.style.left = Math.min(rect.left + window.scrollX, window.innerWidth - pop.offsetWidth - 8) + 'px';
  activeSlotsPopover = pop;
  pop.addEventListener('mouseenter', function () {
    if (slotsPopoverLeaveTimer) {
      clearTimeout(slotsPopoverLeaveTimer);
      slotsPopoverLeaveTimer = null;
    }
  });
  pop.addEventListener('mouseleave', function () {
    if (activeSlotsPopover) {
      activeSlotsPopover.remove();
      activeSlotsPopover = null;
    }
  });
}

export function bindNextRunStatCard(root, slots) {
  const card = root.querySelector('.stat-card-next-run[data-has-slots-popover]');
  if (!card || !slots || slots.length <= 2) return;

  card.addEventListener('mouseenter', function () {
    showSlotsPopover(card, slots);
  });
  card.addEventListener('mouseleave', function () {
    slotsPopoverLeaveTimer = setTimeout(function () {
      if (activeSlotsPopover) {
        activeSlotsPopover.remove();
        activeSlotsPopover = null;
      }
    }, 120);
  });
  card.addEventListener('click', function (e) {
    e.stopPropagation();
    if (activeSlotsPopover) {
      closePopovers();
    } else {
      showSlotsPopover(card, slots);
    }
  });
}

export function closePopovers() {
  if (activeErrorPopover) {
    activeErrorPopover.remove();
    activeErrorPopover = null;
  }
  if (activeSlotsPopover) {
    activeSlotsPopover.remove();
    activeSlotsPopover = null;
  }
}

export function initGlobalDismiss() {
  // width changes (rotation, devtools, desktop<->mobile breakpoint) can happen
  // while the panel is open; re-run the same logic that open/content-switch
  // already uses so a stale desktop inline max-height never survives into
  // the mobile bottom sheet (or vice versa). Deferred a frame, same as
  // layout.js's own resize handling, so html.mobile-layout has already been
  // flipped (initLayout's listener is bound first, layout.js) by the time
  // this reads it.
  window.addEventListener('resize', function () {
    if (!openHistFilterSelect) return;
    requestAnimationFrame(function () {
      if (openHistFilterSelect) positionHistFilterPanel(openHistFilterSelect);
    });
  });

  document.addEventListener('click', function (e) {
    closePopovers();
    if (openCustomSelect && !openCustomSelect.contains(e.target)) closeOpenCustomSelect();
    if (openHistFilterSelect && !openHistFilterSelect.contains(e.target)) closeOpenHistFilterSelect();
    if (openNameFilterWrap && !openNameFilterWrap.contains(e.target)) closeOpenNameFilter();
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      const histPanel = openHistFilterSelect && openHistFilterSelect.querySelector('.hist-filter-panel');
      if (openCustomSelect) {
        // a select layer open elsewhere on the page absorbs the first Escape
        closeOpenCustomSelect();
      } else if (histPanel && histPanel.dataset.filterGroup) {
        // a group list open inside the filter popup absorbs the first Escape;
        // the popup itself only closes on the next press
        renderHistFilterMain(openHistFilterSelect);
      } else {
        closeOpenHistFilterSelect();
      }
      closeOpenNameFilter();
    }
  });
}


export function initChipInput(chipEl, hintsEl, initialValues, suggestions, onChange) {
  const chips = (initialValues || []).slice();
  const chipsEl = chipEl.querySelector('.chip-input-chips');
  const input = chipEl.querySelector('.chip-input-field');

  function notifyChange() {
    if (onChange) onChange(chips.slice());
  }

  function renderChips() {
    chipsEl.innerHTML = '';
    chips.forEach(function (val) {
      const chip = document.createElement('span');
      chip.className = 'chip';
      chip.innerHTML = html`<span class="chip-label">${val}</span><button type="button" class="chip-remove" aria-label="${t('common.remove')}">&times;</button>`;
      chip.querySelector('.chip-remove').addEventListener('click', function () {
        const i = chips.indexOf(val);
        if (i !== -1) chips.splice(i, 1);
        renderChips();
        renderHints();
        notifyChange();
      });
      chipsEl.appendChild(chip);
    });
  }

  function addChip(raw) {
    const val = String(raw || '').trim();
    if (!val || chips.indexOf(val) !== -1) return;
    chips.push(val);
    renderChips();
    renderHints();
    input.value = '';
    notifyChange();
  }

  function renderHints() {
    if (!hintsEl) return;
    hintsEl.innerHTML = '';
    (suggestions || []).forEach(function (cat) {
      const selected = chips.indexOf(cat) !== -1;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'chip-hint' + (selected ? ' chip-hint-selected' : '');
      btn.textContent = cat;
      btn.disabled = selected;
      if (!selected) {
        btn.addEventListener('click', function () { addChip(cat); });
      }
      hintsEl.appendChild(btn);
    });
  }

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      addChip(input.value.replace(/,/g, ''));
    } else if (e.key === 'Backspace' && !input.value && chips.length) {
      chips.pop();
      renderChips();
      renderHints();
      notifyChange();
    }
  });

  input.addEventListener('blur', function () {
    if (input.value.trim()) addChip(input.value);
  });

  renderChips();
  renderHints();

  return {
    getValues: function () { return chips.slice(); },
    setSuggestions: function (list) {
      suggestions = list || [];
      renderHints();
    },
  };
}


export function paginationButtons(prevId, nextId, prevDisabled, nextDisabled) {
  return html`<button type="button" class="btn btn-sm btn-ghost btn-pagination" id="${prevId}"${prevDisabled ? ' disabled' : ''} aria-label="${t('common.pagePrev')}">‹</button><button type="button" class="btn btn-sm btn-ghost btn-pagination" id="${nextId}"${nextDisabled ? ' disabled' : ''} aria-label="${t('common.pageNext')}">›</button>`;
}

export function buildPageSizeSelect(currentSize) {
  const options = PAGE_SIZE_OPTIONS.map(function (n) {
    const label = n === 0 ? t('common.all') : String(n);
    return html`<option value="${n}"${currentSize === n ? ' selected' : ''}>${label}</option>`;
  });
  return html`<select class="select select-compact page-size-select" id="hist-page-size" aria-label="${t('common.pageSize')}">${options}</select>`;
}

export function buildSegmentControl(options, activeValue, ariaLabel) {
  let activeIdx = 0;
  options.forEach(function (opt, i) {
    if (opt.value === activeValue) activeIdx = i;
  });
  const btnsParts = options.map(function (opt, i) {
    const isActive = i === activeIdx;
    return html`<button type="button" class="seg-ctrl-btn${isActive ? ' active' : ''}" data-value="${opt.value}"${opt.dataAttr || ''} title="${opt.title}" aria-label="${opt.title}" aria-pressed="${isActive ? 'true' : 'false'}">${opt.icon ? html`<span data-icon="${opt.icon}"></span>` : opt.label || ''}</button>`;
  });
  return html`<div class="seg-ctrl" role="group" aria-label="${ariaLabel}" data-active-index="${activeIdx}"><div class="seg-ctrl-thumb" aria-hidden="true"></div>${btnsParts}</div>`;
}

export function updateSegmentThumb(seg) {
  const active = seg.querySelector('.seg-ctrl-btn.active');
  if (!active) return;
  const btns = seg.querySelectorAll('.seg-ctrl-btn');
  let idx = 0;
  btns.forEach(function (b, i) {
    if (b === active) idx = i;
  });
  seg.setAttribute('data-active-index', String(idx));
  seg.style.setProperty('--seg-count', String(btns.length));
  const thumb = seg.querySelector('.seg-ctrl-thumb');
  if (thumb) thumb.style.transform = 'translateX(' + idx * 100 + '%)';
}

export function initSegmentControls(root) {
  (root || document).querySelectorAll('.seg-ctrl').forEach(function (seg) {
    if (seg.dataset.segBound) return;
    seg.dataset.segBound = '1';
    updateSegmentThumb(seg);
    seg.querySelectorAll('.seg-ctrl-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        if (btn.classList.contains('active')) return;
        seg.querySelectorAll('.seg-ctrl-btn').forEach(function (b) {
          b.classList.remove('active');
          b.setAttribute('aria-pressed', 'false');
        });
        btn.classList.add('active');
        btn.setAttribute('aria-pressed', 'true');
        updateSegmentThumb(seg);
        seg.dispatchEvent(new CustomEvent('segchange', { bubbles: true, detail: { value: btn.getAttribute('data-value') } }));
      });
    });
  });
}

export function bindPageSizeSelect(root, onChange) {
  const sel = (root || document).querySelector('.page-size-select');
  if (!sel || sel.dataset.pageSizeBound) return;
  sel.dataset.pageSizeBound = '1';
  initCustomSelects(root);
  sel.addEventListener('change', function () {
    const n = parseInt(sel.value, 10);
    const size = PAGE_SIZE_OPTIONS.indexOf(n) >= 0 ? n : 10;
    savePageSize(size);
    onChange(size);
  });
}


export function initPasswordToggles(root) {
  (root || document).querySelectorAll('.input-password-wrap').forEach(function (wrap) {
    if (wrap.dataset.passwordInit) return;
    wrap.dataset.passwordInit = '1';
    const input = wrap.querySelector('.input');
    const btn = wrap.querySelector('.input-password-toggle');
    if (!input || !btn) return;
    btn.addEventListener('click', function () {
      const revealed = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      if (input.classList.contains('input-secure')) {
        input.classList.toggle('input-secure-revealed', !revealed);
      } else {
        input.type = revealed ? 'password' : 'text';
      }
      const nowVisible = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      const iconEl = btn.querySelector('[data-icon]');
      if (iconEl) {
        iconEl.setAttribute('data-icon', nowVisible ? 'eyeOff' : 'eye');
        iconEl.innerHTML = ICONS[nowVisible ? 'eyeOff' : 'eye'];
      }
      btn.setAttribute('aria-label', t(nowVisible ? 'trackers.hideCredentials' : 'trackers.showCredentials'));
    });
  });
}


