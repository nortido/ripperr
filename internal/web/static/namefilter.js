import { fetchJSON, showToast } from './api.js';
import { buildHistoryQuery, dashboardFetchGen, loadDashboard, nextDashboardFetchGen } from './dashboard.js';
import { closeOpenHistFilterSelect } from './histfilter.js';
import { hydrateIcons } from './icons.js';
import { syncPlanForceLocal, updateDashboardTable } from './releases.js';
import { syncDashboardFilterHash } from './router.js';
import { closeOpenCustomSelect } from './select.js';
import { historyState, isMobileStackedLayout, overlayBridge } from './state.js';
import { html } from './util.js';

export let openNameFilterWrap = null;
export let nameFilterDebounceTimer = null;

export function nameFilterHasValue() {
  if ((historyState.name || '').trim()) return true;
  if (openNameFilterWrap) {
    const input = openNameFilterWrap.querySelector('.name-filter-input, .mobile-name-filter-input');
    if (input && input.value.trim()) return true;
  }
  return false;
}

export const MOBILE_SEARCH_CLOSE_MS = 320;
export let mobileSearchAnimToken = 0;

export function closeMobileNameSearchPanel(root, force) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  if (!force && nameFilterHasValue()) return;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  if (!header || !slot) return;

  mobileSearchAnimToken += 1;
  const token = mobileSearchAnimToken;

  function finishClose() {
    if (token !== mobileSearchAnimToken) return;
    header.classList.remove('is-search-animating');
    if (openNameFilterWrap === slot) openNameFilterWrap = null;
  }

  if (!header.classList.contains('is-search-expanded') && !slot.classList.contains('is-open')) {
    finishClose();
    return;
  }

  if (btn) btn.setAttribute('aria-expanded', 'false');
  slot.classList.remove('is-open');
  header.classList.remove('is-search-expanded');

  if (!isMobileStackedLayout()) {
    finishClose();
    return;
  }

  header.classList.add('is-search-animating');

  const shift = scope.querySelector('.card-header-toolbar-shift');
  let done = false;
  function onShiftTransitionEnd(e) {
    if (!shift || e.target !== shift || e.propertyName !== 'transform') return;
    if (done) return;
    done = true;
    shift.removeEventListener('transitionend', onShiftTransitionEnd);
    finishClose();
  }
  if (shift) shift.addEventListener('transitionend', onShiftTransitionEnd);
  setTimeout(function () {
    if (done) return;
    done = true;
    if (shift) shift.removeEventListener('transitionend', onShiftTransitionEnd);
    finishClose();
  }, MOBILE_SEARCH_CLOSE_MS + 40);
}

export function openMobileNameSearchPanel(root, focus) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope || !isMobileStackedLayout()) return;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  const input = scope.querySelector('.mobile-name-filter-input');
  if (!header || !slot || !btn || !input) return;
  if (header.classList.contains('is-search-animating')) return;
  closeOpenCustomSelect();
  closeOpenHistFilterSelect();
  if (openNameFilterWrap && !openNameFilterWrap.classList.contains('mobile-name-search-slot')) {
    closeOpenNameFilter(true);
  }

  mobileSearchAnimToken += 1;
  const token = mobileSearchAnimToken;

  if (slot.classList.contains('is-open') && header.classList.contains('is-search-expanded')) {
    openNameFilterWrap = slot;
    input.value = historyState.name;
    btn.setAttribute('aria-expanded', 'true');
    if (focus) {
      input.focus();
      const len = input.value.length;
      input.setSelectionRange(len, len);
    }
    return;
  }

  slot.classList.remove('is-open');
  header.classList.remove('is-search-expanded', 'is-search-animating');
  header.classList.add('is-search-animating');
  btn.setAttribute('aria-expanded', 'true');
  openNameFilterWrap = slot;
  input.value = historyState.name;

  requestAnimationFrame(function () {
    requestAnimationFrame(function () {
      if (token !== mobileSearchAnimToken) return;
      header.classList.add('is-search-expanded');
      slot.classList.add('is-open');
      const shift = scope.querySelector('.card-header-toolbar-shift');
      let done = false;
      function finishOpen() {
        if (token !== mobileSearchAnimToken) return;
        header.classList.remove('is-search-animating');
      }
      function onShiftTransitionEnd(e) {
        if (!shift || e.target !== shift || e.propertyName !== 'transform') return;
        if (done) return;
        done = true;
        shift.removeEventListener('transitionend', onShiftTransitionEnd);
        finishOpen();
      }
      if (shift) shift.addEventListener('transitionend', onShiftTransitionEnd);
      setTimeout(function () {
        if (done) return;
        done = true;
        if (shift) shift.removeEventListener('transitionend', onShiftTransitionEnd);
        finishOpen();
      }, MOBILE_SEARCH_CLOSE_MS + 40);
      if (focus) {
        input.focus();
        const len = input.value.length;
        input.setSelectionRange(len, len);
      }
    });
  });
}

export function closeDesktopNameFilterPanel(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  const wrap = scope.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  if (!wrap) return;
  wrap.classList.remove('is-open');
  if (label) label.setAttribute('aria-expanded', 'false');
  if (openNameFilterWrap === wrap) openNameFilterWrap = null;
}

export function openDesktopNameFilterPanel(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope || isMobileStackedLayout()) return;
  const wrap = scope.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  const input = wrap && wrap.querySelector('.name-filter-input');
  if (!wrap || !label || !input) return;
  closeMobileNameSearchPanel(scope, true);
  wrap.classList.add('is-open');
  label.setAttribute('aria-expanded', 'true');
  openNameFilterWrap = wrap;
  input.value = historyState.name;
}

export function syncNameFilterLayout() {
  if (!(historyState.name || '').trim()) return;
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.table-dash-releases')) return;
  if (isMobileStackedLayout()) {
    closeDesktopNameFilterPanel(el);
    openMobileNameSearchPanel(el, false);
  } else {
    closeMobileNameSearchPanel(el, true);
    openDesktopNameFilterPanel(el);
  }
  updateNameFilterHeader(el);
}

export function closeOpenNameFilter(force) {
  if (!openNameFilterWrap) return;
  if (!force && nameFilterHasValue()) return;
  if (openNameFilterWrap.classList.contains('mobile-name-search-slot')) {
    closeMobileNameSearchPanel(document.getElementById('view-dashboard'), true);
  } else {
    const label = openNameFilterWrap.querySelector('.th-name-filter-label');
    openNameFilterWrap.classList.remove('is-open');
    if (label) label.setAttribute('aria-expanded', 'false');
    openNameFilterWrap = null;
  }
  if (nameFilterDebounceTimer) {
    clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = null;
  }
}
overlayBridge.closeNameFilter = closeOpenNameFilter;

export async function applyNameFilter(value) {
  const trimmed = (value || '').trim().slice(0, 200);
  if (trimmed === historyState.name) return;
  const keepOpen = openNameFilterWrap !== null;
  historyState.name = trimmed;
  historyState.offset = 0;
  syncDashboardFilterHash();
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.table-dash-releases')) {
    loadDashboard();
    return;
  }
  const fetchGen = nextDashboardFetchGen();
  try {
    const history = await fetchJSON(buildHistoryQuery());
    if (fetchGen !== dashboardFetchGen) return;
    syncPlanForceLocal(history.items || []);
    updateDashboardTable(el, history);
    updateNameFilterHeader(el);
    if (keepOpen || trimmed) restoreNameFilterPanel(el);
  } catch (err) {
    showToast(err.message, 'error');
  }
}

export function updateNameFilterHeader(el) {
  const th = el.querySelector('th.col-name');
  const wrap = el.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  const input = wrap && wrap.querySelector('.name-filter-input');
  if (!th || !wrap || !label) return;
  const active = !!historyState.name;
  th.classList.toggle('is-active', active);
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.col.name');
  label.setAttribute('title', title);
  if (input) input.value = historyState.name;
  let funnel = label.querySelector('.th-name-filter-icon-active');
  if (active && !funnel) {
    funnel = document.createElement('span');
    funnel.className = 'th-name-filter-icon th-name-filter-icon-active';
    funnel.setAttribute('data-icon', 'funnel');
    funnel.setAttribute('aria-hidden', 'true');
    label.appendChild(funnel);
    hydrateIcons(label);
  } else if (!active && funnel) {
    funnel.remove();
  }
  updateMobileNameSearchUI(el);
}

export function updateMobileNameSearchUI(el) {
  const root = el || document.getElementById('view-dashboard');
  if (!root) return;
  const btn = root.querySelector('#dash-mobile-name-search-btn');
  const slot = root.querySelector('.mobile-name-search-slot');
  const header = root.querySelector('.releases-card-header');
  const input = root.querySelector('.mobile-name-filter-input');
  if (!btn || !slot) return;
  const active = !!historyState.name;
  btn.classList.toggle('is-active', active);
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.nameFilterPlaceholder');
  btn.setAttribute('title', title);
  btn.setAttribute('aria-label', title);
  let dot = btn.querySelector('.mobile-name-search-dot');
  if (active && !dot) {
    dot = document.createElement('span');
    dot.className = 'mobile-name-search-dot';
    dot.setAttribute('aria-hidden', 'true');
    btn.appendChild(dot);
  } else if (!active && dot) {
    dot.remove();
  }
  if (input) input.value = historyState.name;
  if (header && active && isMobileStackedLayout()) header.classList.add('is-search-expanded');
  else if (header && !active && !slot.classList.contains('is-open')) {
    header.classList.remove('is-search-expanded');
  }
  if (active && isMobileStackedLayout()) slot.classList.add('is-open');
}

export function restoreNameFilterPanel(el) {
  if (isMobileStackedLayout()) {
    openMobileNameSearchPanel(el, true);
    return;
  }
  openDesktopNameFilterPanel(el);
  const input = el.querySelector('.name-filter-input');
  if (input) {
    input.focus();
    const len = input.value.length;
    input.setSelectionRange(len, len);
  }
}

export function buildNameColumnHeader() {
  const active = !!historyState.name;
  const thClass = 'col-name' + (active ? ' is-active' : '');
  const label = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.col.name');
  const activeIconHtml = active
    ? html`<span class="th-name-filter-icon th-name-filter-icon-active" data-icon="funnel" aria-hidden="true"></span>`
    : '';
  return html`<th class="${thClass}"><div class="name-filter-wrap"><span class="th-name-filter-label" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="${label}"><span class="th-name-filter-text">${t('dash.col.name')}</span><span class="th-name-filter-search" data-icon="search" aria-hidden="true"></span>${activeIconHtml}</span><div class="name-filter-inline"><input type="text" class="name-filter-input" value="${historyState.name}" placeholder="${t('dash.nameFilterPlaceholder')}" autocomplete="off"><button type="button" class="name-filter-clear" title="${t('dash.nameFilterClear')}" aria-label="${t('dash.nameFilterClear')}"><span data-icon="x"></span></button></div></div></th>`;
}

export function bindNameFilter(root) {
  const wrap = (root || document).querySelector('.name-filter-wrap');
  if (!wrap || wrap.dataset.nameFilterBound) return;
  wrap.dataset.nameFilterBound = '1';

  const label = wrap.querySelector('.th-name-filter-label');
  const input = wrap.querySelector('.name-filter-input');
  const clearBtn = wrap.querySelector('.name-filter-clear');
  if (!label || !input) return;

  function closePanel(force) {
    if (!force && nameFilterHasValue()) return;
    wrap.classList.remove('is-open');
    label.setAttribute('aria-expanded', 'false');
    if (openNameFilterWrap === wrap) openNameFilterWrap = null;
    if (nameFilterDebounceTimer) {
      clearTimeout(nameFilterDebounceTimer);
      nameFilterDebounceTimer = null;
    }
  }

  function openPanel() {
    closeOpenCustomSelect();
    closeOpenHistFilterSelect();
    if (openNameFilterWrap && openNameFilterWrap !== wrap) closeOpenNameFilter();
    wrap.classList.add('is-open');
    label.setAttribute('aria-expanded', 'true');
    openNameFilterWrap = wrap;
    input.value = historyState.name;
    input.focus();
    input.select();
  }

  label.addEventListener('click', function (e) {
    e.stopPropagation();
    if (wrap.classList.contains('is-open')) closePanel();
    else openPanel();
  });

  label.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!wrap.classList.contains('is-open')) openPanel();
      else closePanel();
    }
  });

  input.addEventListener('input', function () {
    if (nameFilterDebounceTimer) clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = setTimeout(function () {
      nameFilterDebounceTimer = null;
      applyNameFilter(input.value);
    }, 400);
  });

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      applyNameFilter(input.value);
      closePanel();
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      input.value = '';
      await applyNameFilter('');
      closePanel(true);
    });
  }

  if (historyState.name && !wrap.classList.contains('is-open') && !isMobileStackedLayout()) {
    openPanel();
  }
}

export function buildMobileNameSearchSlot() {
  const active = !!historyState.name;
  const slotCls = 'mobile-name-search-slot' + (active ? ' is-open' : '');
  const btnCls = 'btn btn-sm btn-ghost btn-icon btn-mobile-name-search' + (active ? ' is-active' : '');
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.nameFilterPlaceholder');
  const dotHtml = active ? html`<span class="mobile-name-search-dot" aria-hidden="true"></span>` : '';
  const headerExpanded = active ? ' is-search-expanded' : '';
  return {
    headerClass: headerExpanded,
    // .mobile-name-search-toggle лежит рядом со slot, не внутри: блок кнопок с фиксированной
    // позицией не зависит от анимации поля, и кнопка поиска не сдвинется
    html: html`<div class="${slotCls}"><div class="mobile-name-search-field"><input type="text" class="name-filter-input mobile-name-filter-input" value="${historyState.name}" placeholder="${t('dash.nameFilterPlaceholder')}" autocomplete="off"></div></div><div class="mobile-name-search-toggle"><button type="button" class="btn btn-sm btn-icon name-filter-clear mobile-name-filter-clear" title="${t('dash.nameFilterClear')}" aria-label="${t('dash.nameFilterClear')}"><span data-icon="x"></span></button><button type="button" class="${btnCls}" id="dash-mobile-name-search-btn" aria-haspopup="true" aria-expanded="${active ? 'true' : 'false'}" title="${title}" aria-label="${title}"><span data-icon="search"></span>${dotHtml}</button></div>`,
  };
}

export function bindMobileNameSearch(root) {
  const scope = root || document;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  const input = scope.querySelector('.mobile-name-filter-input');
  const clearBtn = scope.querySelector('.mobile-name-filter-clear');
  if (!header || !slot || !btn || !input || slot.dataset.mobileNameSearchBound) return;
  slot.dataset.mobileNameSearchBound = '1';

  function closePanel(force) {
    if (nameFilterDebounceTimer) {
      clearTimeout(nameFilterDebounceTimer);
      nameFilterDebounceTimer = null;
    }
    closeMobileNameSearchPanel(scope, force);
  }

  function openPanel() {
    openMobileNameSearchPanel(scope, true);
    input.select();
  }

  btn.addEventListener('click', function (e) {
    e.stopPropagation();
    if (header.classList.contains('is-search-animating')) return;
    if (slot.classList.contains('is-open')) closePanel();
    else openPanel();
  });

  input.addEventListener('input', function () {
    if (nameFilterDebounceTimer) clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = setTimeout(function () {
      nameFilterDebounceTimer = null;
      applyNameFilter(input.value);
    }, 400);
  });

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      applyNameFilter(input.value);
      closePanel();
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      input.value = '';
      await applyNameFilter('');
      closePanel(true);
    });
  }

  if (historyState.name && isMobileStackedLayout()) {
    openMobileNameSearchPanel(scope, false);
  }
}
