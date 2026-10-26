import { fetchJSON, showToast } from './api.js';
import { activeErrorPopover, cancelClose, closeAnimated, closePopovers } from './components.js';
import { hydrateIcons, ICONS } from './icons.js';
import { closeOpenCustomSelect, initCustomSelects, openCustomSelect } from './select.js';
import { html } from './util.js';

const ERROR_KEYS = {
  bad_url: 'dash.addBadUrl',
  tracker_unknown: 'dash.addTrackerUnknown',
  no_torrent: 'dash.addNoTorrent',
  already_added: 'dash.addAlreadyAdded',
  not_watched: 'dash.addNotWatched',
  torrent_busy: 'dash.redownloadBusy',
  runner_unavailable: 'dash.runnerNotReady',
  timeout: 'dash.timeout',
};

let dialog = null;
let closingOverlay = null;
let inflight = 0;

export function syncAddButton() {
  const btn = document.getElementById('dash-add-btn');
  if (!btn) return;
  const on = inflight > 0;
  btn.classList.toggle('is-spinning', on);
  btn.disabled = on;
  if (on) btn.setAttribute('aria-busy', 'true');
  else btn.removeAttribute('aria-busy');
  const iconEl = btn.querySelector('[data-icon]');
  if (!iconEl) return;
  const name = on ? 'loader' : 'plus';
  iconEl.setAttribute('data-icon', name);
  iconEl.innerHTML = ICONS[name];
}

function addErrorText(err) {
  const key = ERROR_KEYS[err && err.code];
  return key ? t(key) : (err && err.message ? err.message : t('dash.addFailedTitle'));
}

function setAppInert(on) {
  // верхняя полоса мобильной версии остается живой, как в диалоге трекеров
  document.querySelectorAll('.app > :not(.mobile-header)').forEach(function (el) { el.inert = on; });
}

function dialogKeydown(e) {
  if (e.key !== 'Escape' || !dialog) return;
  // открытый поповер или селект закроет глобальный обработчик, первый Escape его
  if (activeErrorPopover || openCustomSelect) return;
  closeAddReleaseDialog();
}

export function closeAddReleaseDialog(opts) {
  if (closingOverlay) {
    cancelClose(closingOverlay);
    closingOverlay.remove();
    closingOverlay = null;
  }
  const d = dialog;
  if (!d) return;
  dialog = null;
  d.closed = true;
  document.removeEventListener('keydown', dialogKeydown, true);
  closePopovers();
  closeOpenCustomSelect();
  setAppInert(false);
  document.documentElement.classList.remove('modal-open');
  if (d.opener && d.opener.isConnected) d.opener.focus();
  if (opts && opts.force) {
    cancelClose(d.overlay);
    d.overlay.remove();
    return;
  }
  closingOverlay = d.overlay;
  closeAnimated(d.overlay, function () {
    d.overlay.remove();
    if (closingOverlay === d.overlay) closingOverlay = null;
  });
}

function showError(d, text) {
  d.errorLine.textContent = text;
  d.errorLine.hidden = !text;
}

function fillOptions(d, data) {
  const tags = data.mode === 'tags';
  const list = Array.isArray(data.options) ? data.options : [];
  const parts = [];
  if (!tags && data.any) parts.push(html`<option value="">${t('dash.addNoCategory')}</option>`);
  list.forEach(function (name) { parts.push(html`<option value="${name}">${name}</option>`); });
  d.tags = tags;
  d.select.innerHTML = html`${parts}`;
  d.selectLabel.textContent = tags ? t('dash.addTag') : t('dash.addCategory');
  d.selectGroup.hidden = parts.length === 0;
  initCustomSelects(d.selectGroup);
}

async function loadOptions(d) {
  try {
    const data = await fetchJSON('/api/history/add');
    if (d.closed) return;
    fillOptions(d, data || {});
    d.saveBtn.disabled = false;
  } catch (err) {
    if (d.closed) return;
    showError(d, t('dash.addOptionsFailed', { error: err && err.message ? err.message : '' }));
  }
}

async function runAdd(body, onDone) {
  inflight++;
  syncAddButton();
  try {
    const res = await fetchJSON('/api/history/add', { method: 'POST', body: JSON.stringify(body) });
    if (res && res.unignored) showToast(t('dash.addDoneUnignored'), 'success');
    // спиннер гасим только после обновления списка, чтобы строка уже была видна
    if (onDone) await onDone();
  } catch (err) {
    showToast(addErrorText(err), 'error', { title: t('dash.addFailedTitle') });
  } finally {
    inflight--;
    syncAddButton();
  }
}

function submitAdd(d) {
  if (d.saveBtn.disabled) return;
  const url = d.urlInput.value.trim();
  if (!url) {
    d.urlInput.focus();
    return;
  }
  const body = { url: url };
  if (!d.selectGroup.hidden) body[d.tags ? 'tag' : 'category'] = d.select.value;
  const onDone = d.onDone;
  closeAddReleaseDialog();
  runAdd(body, onDone);
}

export function openAddReleaseDialog(opener, onDone) {
  closeAddReleaseDialog({ force: true });
  const titleId = 'add-release-title';
  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  overlay.innerHTML = html`<div class="modal" role="dialog" aria-modal="true" aria-labelledby="${titleId}" tabindex="-1"><div class="modal-head"><h2 class="modal-title" id="${titleId}">${t('dash.add')}</h2><button type="button" class="btn btn-ghost btn-icon-only modal-close" title="${t('common.close')}" aria-label="${t('common.close')}"><span data-icon="x"></span></button></div><div class="modal-body"><div class="form-group"><label class="form-label" for="add-release-url">${t('dash.addUrl')}</label><input type="url" id="add-release-url" class="input" inputmode="url" autocomplete="off" autocapitalize="off" spellcheck="false"></div><div class="form-group add-release-select-group" hidden><label class="form-label" for="add-release-select"></label><select id="add-release-select" class="select"></select></div></div><p class="test-result modal-test-result add-release-error" role="alert" hidden></p><div class="modal-foot"><button type="button" class="btn btn-primary btn-add-release-submit" disabled>${t('dash.addSubmit')}</button></div></div>`;
  const root = overlay.querySelector('.modal');
  const d = {
    overlay: overlay,
    opener: opener,
    onDone: onDone,
    closed: false,
    tags: false,
    urlInput: root.querySelector('#add-release-url'),
    select: root.querySelector('#add-release-select'),
    selectGroup: root.querySelector('.add-release-select-group'),
    selectLabel: root.querySelector('.add-release-select-group .form-label'),
    errorLine: root.querySelector('.add-release-error'),
    saveBtn: root.querySelector('.btn-add-release-submit'),
  };
  dialog = d;

  d.saveBtn.addEventListener('click', function () { submitAdd(d); });
  d.urlInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.isComposing) {
      e.preventDefault();
      submitAdd(d);
    }
  });
  root.querySelector('.modal-close').addEventListener('click', function () { closeAddReleaseDialog(); });

  let downOnBackdrop = false;
  overlay.addEventListener('pointerdown', function (e) { downOnBackdrop = e.target === overlay; });
  overlay.addEventListener('click', function (e) {
    if (e.target === overlay && downOnBackdrop) closeAddReleaseDialog();
    downOnBackdrop = false;
  });
  document.addEventListener('keydown', dialogKeydown, true);

  document.body.appendChild(overlay);
  setAppInert(true);
  document.documentElement.classList.add('modal-open');
  hydrateIcons(overlay);
  d.urlInput.focus();
  loadOptions(d);
}
