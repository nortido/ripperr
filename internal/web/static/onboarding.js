import { fetchJSON, showToast } from './api.js';
import { dashboardHasActiveInteraction } from './dashboard.js';
import { hydrateIcons } from './icons.js';
import { currentView, showView } from './router.js';
import { startTour } from './tour.js';
import { html } from './util.js';

const TOUR_DELAY_MS = 300;
const NAV_WAIT_MS = 5000;

let onbState = null;
let onbGen = 0;
let tourTimer = null;
let dismissedThisPage = false;

export function clearOnboardingDismiss() {
  dismissedThisPage = false;
}

function saveOnboarding(patch) {
  return fetchJSON('/api/onboarding', { method: 'PUT', body: JSON.stringify(patch) });
}

export async function refreshOnboarding() {
  // Запрос только при входе на дашборд (loadDashboard). Перерисовка раз в 30 секунд и опрос
  // статуса берут состояние из кеша, иначе каждая из них дергала бы проверку клиента
  const gen = ++onbGen;
  let data = null;
  try {
    data = await fetchJSON('/api/onboarding');
  } catch (_) { /* карточка не критична: без ответа ничего не показываем */ }
  if (gen !== onbGen || currentView !== 'dashboard') return;
  if (!data) {
    onbState = null;
    renderOnboardingCard();
    return;
  }
  onbState = {
    checklist_hidden: !!data.checklist_hidden,
    tour_done: !!data.tour_done,
    replay: !!data.replay,
    client_ok: !!data.client_ok,
    tracker_ok: !!data.tracker_ok,
    watch_ok: !!data.watch_ok,
  };
  if (dismissedThisPage) {
    // неудавшийся PUT не должен возвращать карточку, пока страница открыта
    onbState.checklist_hidden = true;
    onbState.replay = false;
  }
  if (!onbState.checklist_hidden && !onbState.replay && onbState.client_ok && onbState.tracker_ok && onbState.watch_ok) {
    // все три готовы: помечаем скрытой, чтобы дальше сервер не опрашивал клиент
    onbState.checklist_hidden = true;
    saveOnboarding({ checklist_hidden: true }).catch(function () { /* повторим при следующем входе */ });
  }
  renderOnboardingCard();
  maybeStartDashboardTour();
}

export function onboardingCardHtml() {
  if (!onbState || onbState.checklist_hidden) return '';
  const items = [
    { id: 'client', done: onbState.client_ok, title: t('onb.client'), hint: t('onb.clientHint'), go: t('onb.clientGo') },
    { id: 'tracker', done: onbState.tracker_ok, title: t('onb.tracker'), hint: t('onb.trackerHint'), go: t('onb.trackerGo') },
    { id: 'watch', done: onbState.watch_ok, title: t('onb.watch'), hint: t('onb.watchHint'), go: t('onb.watchGo') },
  ];
  const total = items.length;
  const done = items.filter(function (item) { return item.done; }).length;
  const rows = items.map(function (item) {
    return html`<li class="onb-row${item.done ? ' onb-row-done' : ''}"><span class="onb-dot" aria-hidden="true">${item.done ? html`<span data-icon="check"></span>` : ''}</span><div class="onb-text"><div class="onb-title">${item.title}</div><div class="onb-hint">${item.hint}</div></div>${item.done ? '' : html`<button type="button" class="btn btn-primary btn-sm onb-go" data-onb-go="${item.id}">${item.go}</button>`}</li>`;
  });
  return html`<div class="card onb-card" id="onboarding-card"><div class="card-header"><h2 class="card-title">${t('onb.title')}</h2><span class="onb-count text-muted">${t('onb.progress', { done: done, total: total })}</span><button type="button" class="btn btn-ghost btn-icon-only onb-hide" aria-label="${t('onb.hide')}" title="${t('onb.hide')}"><span data-icon="x"></span></button></div><div class="card-body"><div class="onb-bar" role="progressbar" aria-label="${t('onb.title')}" aria-valuemin="0" aria-valuemax="${total}" aria-valuenow="${done}"><div class="onb-bar-fill" style="width:${Math.round(done * 100 / total)}%"></div></div><ul class="onb-list">${rows}</ul></div></div>`;
}

export function bindOnboardingCard(root) {
  const card = (root || document).querySelector('#onboarding-card');
  if (!card) return;
  const hideBtn = card.querySelector('.onb-hide');
  if (hideBtn) hideBtn.addEventListener('click', dismissChecklist);
  card.querySelectorAll('.onb-go').forEach(function (btn) {
    btn.addEventListener('click', function () { openOnboardingTarget(btn.getAttribute('data-onb-go')); });
  });
}

function renderOnboardingCard() {
  const el = document.getElementById('view-dashboard');
  const header = el && el.querySelector('.page-header');
  if (!header) return;
  const old = el.querySelector('#onboarding-card');
  if (old) old.remove();
  const markup = onboardingCardHtml();
  if (!markup) return;
  header.insertAdjacentHTML('afterend', markup.toString());
  hydrateIcons(el.querySelector('#onboarding-card'));
  bindOnboardingCard(el);
}

function dismissChecklist() {
  if (!onbState) return;
  // ответ, который еще в пути, не должен вернуть карточку
  onbGen++;
  dismissedThisPage = true;
  onbState.checklist_hidden = true;
  onbState.replay = false;
  renderOnboardingCard();
  saveOnboarding({ checklist_hidden: true, replay: false }).catch(function (err) { showToast(err.message, 'error'); });
  maybeStartDashboardTour();
}

function whenViewReady(view, selector, fn) {
  // Переход в showView асинхронный (через hashchange), поэтому селектор ищем только когда
  // currentView уже стал нужным, иначе увидим старое содержимое чужой страницы
  const startedAt = performance.now();
  let reached = false;
  function tick() {
    if (currentView === view) reached = true;
    else if (reached) return;
    if (reached) {
      const found = document.querySelector(selector);
      if (found) {
        fn(found);
        return;
      }
    }
    if (performance.now() - startedAt > NAV_WAIT_MS) return;
    requestAnimationFrame(tick);
  }
  requestAnimationFrame(tick);
}

function scrollCardIntoView(el) {
  const card = el.closest('.card') || el;
  card.scrollIntoView({ block: 'start', behavior: 'auto' });
}

function openOnboardingTarget(id) {
  if (id === 'client') {
    showView('integrations');
    whenViewReady('integrations', '#view-integrations #download-client', function (field) {
      scrollCardIntoView(field);
      field.focus({ preventScroll: true });
    });
  } else if (id === 'tracker') {
    showView('trackers');
    whenViewReady('trackers', '#view-trackers .btn-add-tracker-open', function (btn) {
      if (!btn.disabled) btn.click();
    });
  } else if (id === 'watch') {
    showView('schedule');
    whenViewReady('schedule', '#view-schedule #watch-mode', scrollCardIntoView);
  }
}

function firstVisible(selector) {
  const all = document.querySelectorAll(selector);
  for (let i = 0; i < all.length; i++) {
    if (all[i].getClientRects().length > 0) return all[i];
  }
  return null;
}

function tourEligible() {
  if (!onbState || onbState.tour_done || !onbState.checklist_hidden) return false;
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return false;
  if (document.querySelector('.modal-overlay')) return false;
  if (document.querySelector('#sidebar.open, #sidebar.sidebar-overlay-open')) return false;
  if (dashboardHasActiveInteraction()) return false;
  return !!firstVisible('#view-dashboard .release-actions');
}

export function maybeStartDashboardTour() {
  if (tourTimer || !tourEligible()) return;
  // небольшая пауза: раскладка после перерисовки должна успокоиться
  tourTimer = setTimeout(function () {
    tourTimer = null;
    if (!tourEligible()) return;
    startTour([
      { target: function () { return firstVisible('#view-dashboard .release-actions'); }, title: t('tour.actionsTitle'), text: t('tour.actionsText') },
      { target: function () { return document.querySelector('#view-dashboard .stat-card-next-run'); }, title: t('tour.nextRunTitle'), text: t('tour.nextRunText') },
    ], { onClose: onTourClose });
  }, TOUR_DELAY_MS);
}

function onTourClose(reason) {
  if (reason !== 'done' && reason !== 'closed') return;
  if (onbState) onbState.tour_done = true;
  saveOnboarding({ tour_done: true }).catch(function () { /* тихо: обучение может вернуться при следующем входе */ });
}
