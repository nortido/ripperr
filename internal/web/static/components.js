import { fetchJSON, showToast } from './api.js';
import { closeOpenHistFilterSelect, openHistFilterSelect, positionHistFilterPanel, renderHistFilterMain } from './histfilter.js';
import { ICONS } from './icons.js';
import { closeOpenNameFilter, openNameFilterWrap } from './namefilter.js';
import { closeOpenCustomSelect, initCustomSelects, openCustomSelect } from './select.js';
import { PAGE_SIZE_OPTIONS, SECRET_MASK, html, savePageSize } from './util.js';

export let activeErrorPopover = null;
let closingPopover = null;

export const POP_CLOSE_MS = 130;

export function closeAnimated(el, done) {
  // запас 30 мс сверху на длительность выхода (--pop-out-dur в overlays.css)
  if (!el || el.popCloseTimer) return;
  el.classList.add('is-closing');
  el.popCloseTimer = setTimeout(function () {
    el.popCloseTimer = 0;
    el.classList.remove('is-closing');
    done();
  }, POP_CLOSE_MS + 30);
}

export function cancelClose(el) {
  if (!el) return;
  clearTimeout(el.popCloseTimer);
  el.popCloseTimer = 0;
  el.classList.remove('is-closing');
}

export function bindErrorBadges(root) {
  root.querySelectorAll('.status-error-badge').forEach(function (badge) {
    badge.addEventListener('click', function (e) {
      e.stopPropagation();
      const err = badge.getAttribute('data-error') || '';
      closePopovers(true);
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

export function closePopovers(force) {
  // force при открытии нового поповера: еще закрывающийся прежний убираем сразу,
  // иначе он повиснет рядом с новым
  if (force && closingPopover) {
    cancelClose(closingPopover);
    closingPopover.remove();
    closingPopover = null;
  }
  const pop = activeErrorPopover;
  if (!pop) return;
  activeErrorPopover = null;
  if (pop.hintOwner) pop.hintOwner.setAttribute('aria-expanded', 'false');
  clearTimeout(pop.hintTimer);
  if (force) {
    pop.remove();
    return;
  }
  closingPopover = pop;
  closeAnimated(pop, function () {
    pop.remove();
    if (closingPopover === pop) closingPopover = null;
  });
}

export function initGlobalDismiss() {
  // ширина может смениться при открытой панели (поворот, devtools), и десктопный
  // max-height уехал бы в мобильную шторку или наоборот. Ждем кадр, как в layout.js,
  // чтобы html.mobile-layout уже успел переключиться
  window.addEventListener('resize', function () {
    if (!openHistFilterSelect) return;
    requestAnimationFrame(function () {
      if (openHistFilterSelect) positionHistFilterPanel(openHistFilterSelect);
    });
  });

  // на iPhone тап по пустому месту не доходит до document как click, и подсказка
  // висела. Закрываем по касанию мимо
  document.addEventListener('pointerdown', function (e) {
    const pop = activeErrorPopover;
    if (!pop || pop.contains(e.target)) return;
    if (pop.hintOwner && pop.hintOwner.contains(e.target)) return;
    closePopovers();
  }, true);

  document.addEventListener('click', function (e) {
    closePopovers();
    if (openCustomSelect && !openCustomSelect.contains(e.target)) closeOpenCustomSelect();
    if (openHistFilterSelect && !openHistFilterSelect.contains(e.target)) closeOpenHistFilterSelect();
    if (openNameFilterWrap && !openNameFilterWrap.contains(e.target)) closeOpenNameFilter();
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      if (activeErrorPopover) {
        closePopovers();
        return;
      }
      const histPanel = openHistFilterSelect && openHistFilterSelect.querySelector('.hist-filter-panel');
      if (openCustomSelect) {
        // первый Escape забирает селект, открытый где-то на странице
        closeOpenCustomSelect();
      } else if (histPanel && histPanel.dataset.filterGroup) {
        // первый Escape закрывает список групп, сам попап закроется следующим
        renderHistFilterMain(openHistFilterSelect);
      } else {
        closeOpenHistFilterSelect();
      }
      closeOpenNameFilter();
    }
  });
}


export function initChipInput(chipEl, hintsEl, initialValues, suggestions, onChange, opts) {
  const chips = (initialValues || []).slice();
  const decorate = opts && opts.decorate;
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
      if (decorate) decorate(val, chip);
      chip.querySelector('.chip-remove').addEventListener('click', function () { removeChip(val); });
      chipsEl.appendChild(chip);
    });
  }

  function addChip(raw) {
    const val = String(raw || '').trim();
    if (!val) return;
    if (chips.indexOf(val) !== -1) {
      // повтор не добавляем, но и в поле не оставляем: иначе форма считала бы его несохраненной правкой
      if (input.value) {
        input.value = '';
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
      return;
    }
    chips.push(val);
    renderChips();
    renderHints();
    input.value = '';
    notifyChange();
  }

  function removeChip(val) {
    const i = chips.indexOf(val);
    if (i !== -1) chips.splice(i, 1);
    renderChips();
    renderHints();
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
      // закрепленный чип поле может прятать, Backspace его не трогает
      let i = chips.length - 1;
      while (i >= 0 && opts && opts.pinned && opts.pinned(chips[i])) i--;
      if (i < 0) return;
      chips.splice(i, 1);
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
    // в отличие от ввода руками поле не очищаем: там может быть недописанный адрес
    add: function (raw) {
      const val = String(raw || '').trim();
      if (!val || chips.indexOf(val) !== -1) return;
      chips.push(val);
      renderChips();
      renderHints();
      notifyChange();
    },
    remove: function (val) {
      if (chips.indexOf(val) !== -1) removeChip(val);
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
    return html`<button type="button" class="seg-ctrl-btn${isActive ? ' active' : ''}" data-value="${opt.value}"${opt.dataAttr || ''}${opt.icon ? html` title="${opt.title}" aria-label="${opt.title}"` : ''} aria-pressed="${isActive ? 'true' : 'false'}">${opt.icon ? html`<span data-icon="${opt.icon}"></span>` : opt.label || ''}</button>`;
  });
  return html`<div class="seg-ctrl" role="group" aria-label="${ariaLabel}" data-active-index="${activeIdx}"><div class="seg-ctrl-thumb" aria-hidden="true"></div>${btnsParts}</div>`;
}

// имя не влезло: шрифт меньше на 1 px, не больше, дальше пусть режется многоточием
function fitSegmentText(seg) {
  seg.classList.remove('seg-ctrl-tight');
  const cut = Array.from(seg.querySelectorAll('.seg-ctrl-btn *')).some(function (el) {
    return el.clientWidth > 0 && el.scrollWidth > el.clientWidth;
  });
  if (cut) seg.classList.add('seg-ctrl-tight');
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
  if (thumb && seg.classList.contains('seg-ctrl-fit')) {
    // колонки по ширине текста разные: бегунок меряем по самой кнопке и перемеряем,
    // когда меняется ширина (шрифт догрузился, экран повернули)
    if (!seg._fitObserver && window.ResizeObserver) {
      seg._fitObserver = new ResizeObserver(function () { updateSegmentThumb(seg); });
      btns.forEach(function (b) { seg._fitObserver.observe(b); });
    }
    fitSegmentText(seg);
    if (active.offsetWidth) {
      // на телефоне вкладки уведомлений в две строки, бегунок двигаем и по вертикали
      thumb.style.width = active.offsetWidth + 'px';
      thumb.style.height = active.offsetHeight + 'px';
      thumb.style.bottom = 'auto';
      placeSegmentThumb(seg, thumb, 'translate(' + (active.offsetLeft - thumb.offsetLeft) + 'px, ' + (active.offsetTop - thumb.offsetTop) + 'px)');
      return;
    }
  }
  if (thumb) placeSegmentThumb(seg, thumb, 'translateX(' + idx * 100 + '%)');
}

function placeSegmentThumb(seg, thumb, transform) {
  if (seg._thumbPlaced) {
    thumb.style.transform = transform;
    return;
  }
  // каждая перерисовка создает переключатель заново с бегунком слева, и он ехал
  // к выбранной кнопке на глазах. Первую расстановку делаем без анимации
  seg._thumbPlaced = true;
  thumb.style.transition = 'none';
  thumb.style.transform = transform;
  void thumb.offsetWidth;
  thumb.style.transition = '';
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


function hintLabel(hint) {
  const prev = hint.previousElementSibling;
  const row = hint.closest('.form-submit-row') || (prev && prev.matches('.form-submit-row') ? prev : null);
  if (row) {
    const btns = row.querySelectorAll(':scope > .btn');
    if (btns.length) return btns[btns.length - 1];
  }
  const pick = function (el) {
    if (!el) return null;
    const all = el.querySelectorAll('.form-label, .form-switch > span');
    return all.length ? all[all.length - 1] : null;
  };
  // подсказка бывает вне группы: под рядом полей или в карточке, где подпись это заголовок
  const group = hint.closest('.form-group');
  const card = hint.closest('.card');
  return pick(group) || pick(hint.previousElementSibling) || (card && card.querySelector('.card-title'));
}

export function setSaving(btn, on) {
  if (!btn) return;
  btn.disabled = on;
  btn.classList.toggle('is-saving', on);
  btn.setAttribute('aria-busy', on ? 'true' : 'false');
}

export function initHintToggles(root) {
  // пояснения под полями съедали по две-три строки, так что прячем их за кнопку (i).
  // Предупреждения и живые подсказки с id остаются на виду
  (root || document).querySelectorAll('.form-hint:not(.form-hint-warn):not(.form-hint-keep):not([id])').forEach(function (hint) {
    if (hint.dataset.hintInit) return;
    const label = hintLabel(hint);
    if (!label) return;
    hint.dataset.hintInit = '1';
    hint.classList.add('form-hint-collapsed');
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'hint-toggle';
    btn.setAttribute('aria-expanded', 'false');
    btn.setAttribute('aria-label', t('common.moreInfo'));
    btn.innerHTML = ICONS.info;
    btn.addEventListener('click', function (e) {
      // кнопка внутри label переключателя, без этого клик щелкал бы его
      e.preventDefault();
      e.stopPropagation();
      const wasOpen = activeErrorPopover && activeErrorPopover.hintOwner === btn;
      if (wasOpen) {
        closePopovers();
        return;
      }
      closePopovers(true);
      const pop = document.createElement('div');
      pop.className = 'error-popover hint-popover';
      pop.setAttribute('role', 'tooltip');
      pop.textContent = hint.textContent;
      pop.hintOwner = btn;
      document.body.appendChild(pop);
      const rect = btn.getBoundingClientRect();
      const left = Math.max(8, Math.min(rect.left + rect.width / 2 - pop.offsetWidth / 2, window.innerWidth - pop.offsetWidth - 8));
      pop.style.top = (rect.bottom + window.scrollY + 6) + 'px';
      pop.style.left = (left + window.scrollX) + 'px';
      btn.setAttribute('aria-expanded', 'true');
      activeErrorPopover = pop;
      // если про пояснение забыли, пусть закроется само
      pop.hintTimer = setTimeout(function () {
        if (activeErrorPopover === pop) closePopovers();
      }, 10000);
    });
    // у переключателя и кнопки (i) встает соседом в той же flex-строке, внутри span она уехала бы на вторую строку
    // в шапке страницы кнопка прижата вправо, (i) ставим слева от нее
    if (label.matches('.page-header-actions > .btn')) label.before(btn);
    else if (label.matches('.form-switch > span, .form-submit-row > .btn')) label.after(btn);
    else label.appendChild(btn);
    if (label.matches('.form-switch > span')) pinHintToFirstLine(label, btn);
  });
}

function pinHintToFirstLine(span, btn) {
  // перенесенная подпись растягивает span на всю ширину, и (i) уезжала к правому краю.
  // Сдвигаем ее к концу первой строки, ширину строки знает только раскладка браузера
  const place = function () {
    const range = document.createRange();
    range.selectNodeContents(span);
    const first = range.getClientRects()[0];
    const box = span.getBoundingClientRect();
    const shift = first && box.width ? first.right - box.right : 0;
    btn.style.transform = shift < -0.5 ? 'translateX(' + shift + 'px)' : '';
  };
  if (typeof ResizeObserver === 'function') new ResizeObserver(place).observe(span);
  place();
}

const maskedRoots = new WeakSet();

export function initPasswordToggles(root) {
  const scope = root || document;
  if (!maskedRoots.has(scope)) {
    maskedRoots.add(scope);
    // маску меняем только целиком: любая правка сначала чистит поле,
    // иначе выйдут звездочки вперемешку с мусором
    scope.addEventListener('beforeinput', function (e) {
      const target = e.target;
      if (!(target instanceof HTMLInputElement) || !target.value.includes(SECRET_MASK)) return;
      target.value = '';
      if (String(e.inputType).startsWith('delete')) {
        e.preventDefault();
        target.dispatchEvent(new Event('input', { bubbles: true }));
      }
    });
  }
  scope.querySelectorAll('.input-password-wrap').forEach(function (wrap) {
    if (wrap.dataset.passwordInit) return;
    wrap.dataset.passwordInit = '1';
    const input = wrap.querySelector('.input');
    const btn = wrap.querySelector('.input-password-toggle');
    if (!input || !btn) return;
    function setVisible(show) {
      if (input.classList.contains('input-secure')) {
        input.classList.toggle('input-secure-revealed', show);
      } else {
        input.type = show ? 'text' : 'password';
      }
      const iconEl = btn.querySelector('[data-icon]');
      if (iconEl) {
        iconEl.setAttribute('data-icon', show ? 'eye' : 'eyeHidden');
        iconEl.innerHTML = ICONS[show ? 'eye' : 'eyeHidden'];
      }
      btn.setAttribute('aria-label', t(show ? 'trackers.hideCredentials' : 'trackers.showCredentials'));
    }
    btn.addEventListener('click', async function () {
      if (btn.dataset.revealing) return;
      const url = input.dataset.reveal;
      const field = input.dataset.revealField;
      if (input.value === SECRET_MASK && url && field) {
        btn.dataset.revealing = '1';
        try {
          const res = await fetchJSON(url, { method: 'POST', body: JSON.stringify({ field }) });
          // пока шел запрос, поле могли поменять или переключить на другой клиент, тогда ответ уже чужой
          if (input.value === SECRET_MASK && input.dataset.revealField === field) {
            input.value = res.value;
            setVisible(true);
            input.dispatchEvent(new CustomEvent('secretreveal', { bubbles: true, detail: { value: res.value } }));
          }
        } catch (err) {
          showToast(t('common.revealFailed') + ': ' + err.message, 'error');
        } finally {
          delete btn.dataset.revealing;
        }
        return;
      }
      const revealed = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      setVisible(!revealed);
    });
  });
}


