import { fetchJSON, showToast } from './api.js';
import { activeErrorPopover, closePopovers, setActivePopover } from './components.js';
import { hydrateIcons, icon } from './icons.js';
import { formatTableTime, html, statusMeta } from './util.js';

let openSeq = 0;
let teardown = null;

function formatDelay(releasedAt, at) {
  const minutes = Math.max(0, Math.round((new Date(at) - new Date(releasedAt)) / 60000));
  if (minutes < 60) return t('relhist.durM', { m: minutes });
  if (minutes < 1440) return t('relhist.durHM', { h: Math.floor(minutes / 60), m: minutes % 60 });
  return t('relhist.durDH', { d: Math.floor(minutes / 1440), h: Math.floor((minutes % 1440) / 60) });
}

function entryView(e) {
  if (e.kind === 'updated') {
    const sub = e.released_at ? t('relhist.updatedSub', { time: formatTableTime(e.released_at), delay: formatDelay(e.released_at, e.at) }) : '';
    return { cls: 'status-icon-success', icon: 'check', title: t('relhist.updated'), sub: sub, full: '' };
  }
  if (e.kind === 'tracker') {
    return { cls: 'status-icon-success', icon: 'check', title: t('relhist.tracker'), sub: '', full: '' };
  }
  if (e.kind === 'check_failed') {
    return { cls: 'status-icon-danger', icon: 'xCircle', title: t('relhist.failed'), sub: e.text || '', full: e.text || '' };
  }
  const sub = e.released_at ? t('relhist.addedSub', { date: new Date(e.released_at).toLocaleDateString(localeTag()) }) : '';
  return { cls: 'status-icon-info', icon: 'plus', title: t('relhist.added'), sub: sub, full: '' };
}

function rowHtml(e) {
  const v = entryView(e);
  const sub = v.sub ? html`<span class="relhist-sub" title="${v.full}">${v.sub}</span>` : '';
  return html`<li class="relhist-row"><span class="status-icon ${v.cls}">${icon(v.icon)}</span><span class="relhist-text"><span class="relhist-row-title">${v.title}</span>${sub}</span><span class="relhist-time">${formatTableTime(e.at)}</span></li>`;
}

function footerText(d) {
  if (d.status === 'new') return d.recheck_at ? t('dash.firstCheckAt', { time: formatTableTime(d.recheck_at) }) : '';
  if (d.checked_at && d.recheck_at) return t('relhist.checkedNext', { time: formatTableTime(d.checked_at), next: formatTableTime(d.recheck_at) });
  if (d.checked_at) return t('relhist.checked', { time: formatTableTime(d.checked_at) });
  return '';
}

function panelHtml(d) {
  const entries = d.entries || [];
  let current = '';
  if ((d.status === 'error' || d.status === 'unknown') && d.error) {
    const m = statusMeta(d.status);
    current = html`<div class="relhist-current"><div class="relhist-current-head"><span class="${m.cls}">${icon(m.iconName)}</span><span class="relhist-row-title">${m.label}</span></div><div class="relhist-current-text">${d.error}</div></div>`;
  }
  let list = '';
  if (entries.length) list = html`<ul class="relhist-list">${entries.map(rowHtml)}</ul>`;
  else if (!current) list = html`<div class="relhist-empty">${t('relhist.empty')}</div>`;
  const foot = footerText(d);
  const footer = foot ? html`<div class="relhist-foot">${foot}</div>` : '';
  return html`${current}${list}${footer}`;
}

function showDialog(badge, d) {
  const titleId = 'relhist-title';
  const wrap = document.createElement('div');
  wrap.className = 'modal-overlay relhist-overlay';
  wrap.hintOwner = badge;
  const count = d.update_count > 0 ? html`<span class="relhist-count">${t('relhist.count', { n: d.update_count, days: d.period_days })}</span>` : '';
  wrap.innerHTML = String(html`<div class="modal relhist-modal" role="dialog" aria-modal="true" aria-labelledby="${titleId}" tabindex="-1"><div class="modal-head"><div class="relhist-headtext"><h2 class="modal-title" id="${titleId}">${t('relhist.title')}</h2><div class="relhist-name" title="${d.name}">${d.name}</div></div>${count}<button type="button" class="btn btn-ghost btn-icon-only modal-close" title="${t('common.close')}" aria-label="${t('common.close')}"><span data-icon="x"></span></button></div><div class="modal-body">${panelHtml(d)}</div></div>`);
  const box = wrap.querySelector('.relhist-modal');

  // как в confirm.js: inert ставим только там, где его еще не было, и снимаем только свой
  const root = document.documentElement;
  const inerted = [];
  document.querySelectorAll('.app, body > .modal-overlay').forEach(function (el) {
    if (!el.inert) {
      el.inert = true;
      inerted.push(el);
    }
  });
  const addedModalOpen = !root.classList.contains('modal-open');
  if (addedModalOpen) root.classList.add('modal-open');

  let done = false;
  // closePopovers зовет хук при любом закрытии, в том числе снаружи (тап по тосту,
  // перерисовка дашборда): иначе .app остался бы inert
  wrap.onPopClose = function () {
    if (done) return;
    done = true;
    teardown = null;
    window.removeEventListener('keydown', onKey, true);
    wrap.inert = true;
    inerted.forEach(function (el) { el.inert = false; });
    if (addedModalOpen) root.classList.remove('modal-open');
    if (badge.isConnected) badge.focus();
  };
  function close(force) {
    if (activeErrorPopover === wrap) {
      closePopovers(force);
    } else {
      wrap.onPopClose();
      wrap.remove();
    }
  }
  // window в фазе перехвата получает Escape раньше глобального обработчика
  function onKey(e) {
    if (e.key !== 'Escape') return;
    e.preventDefault();
    e.stopPropagation();
    close();
  }
  teardown = function () { close(true); };

  wrap.querySelector('.modal-close').addEventListener('click', function () { close(); });
  // подложка закрывает, только если и нажатие, и отпускание были на ней. Остальные клики
  // не пускаем к document: он закрыл бы любой поповер
  let downOnBackdrop = false;
  wrap.addEventListener('pointerdown', function (e) { downOnBackdrop = e.target === wrap; });
  wrap.addEventListener('click', function (e) {
    e.stopPropagation();
    if (e.target === wrap && downOnBackdrop) close();
    downOnBackdrop = false;
  });
  window.addEventListener('keydown', onKey, true);

  document.body.appendChild(wrap);
  hydrateIcons(wrap);
  badge.setAttribute('aria-expanded', 'true');
  setActivePopover(wrap);
  box.focus();
}

async function openHistory(badge) {
  const row = badge.closest('[data-release-hash]');
  const hash = row ? row.getAttribute('data-release-hash') : '';
  if (!hash) return;
  const seq = ++openSeq;
  let data;
  try {
    data = await fetchJSON('/api/history/' + encodeURIComponent(hash) + '/timeline');
  } catch (err) {
    if (seq === openSeq) showToast(err.message, 'error');
    return;
  }
  if (seq !== openSeq || !badge.isConnected) return;
  if (teardown) teardown();
  closePopovers(true);
  showDialog(badge, data);
}

export function bindReleaseHistory(root) {
  root.querySelectorAll('.status-history-badge').forEach(function (badge) {
    if (badge.dataset.relhistBound) return;
    badge.dataset.relhistBound = '1';
    badge.addEventListener('click', function (e) {
      e.preventDefault();
      e.stopPropagation();
      openHistory(badge);
    });
    badge.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      e.preventDefault();
      e.stopPropagation();
      openHistory(badge);
    });
  });
}
