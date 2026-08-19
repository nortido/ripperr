import { hydrateIcons } from './icons.js';
import { alertIncidentKey, dismissAlertKey, formatTableTime, html, pruneDismissedAlerts, visibleAlerts } from './util.js';

export let dashboardAlerts = [];

export function setDashboardAlerts(alerts) {
  dashboardAlerts = alerts || [];
}

export function renderAlertsHtml(alerts) {
  const visible = visibleAlerts(alerts);
  if (!visible.length) return '';
  const parts = [];
  visible.forEach(function (alert) {
    const key = alertIncidentKey(alert);
    const sinceTime = formatTableTime(alert.since);
    const name = alert.tracker || t('common.emDash');
    parts.push(html`<div class="dash-alert" data-alert-key="${key}"><span class="dash-alert-icon" data-icon="warning"></span><span class="dash-alert-text">${t('dash.alert.trackerDown', { name: name, time: sinceTime, error: alert.error || '', })}</span><button type="button" class="btn btn-ghost btn-icon btn-sm dash-alert-dismiss" data-alert-key="${key}" title="${t('dash.alert.dismiss')}" aria-label="${t('dash.alert.dismiss')}"><span data-icon="x"></span></button></div>`);
  });
  return html`<div class="dash-alerts">${parts}</div>`;
}

export function updateDashboardAlerts(alerts) {
  pruneDismissedAlerts(alerts);
  dashboardAlerts = alerts || [];
  const el = document.getElementById('view-dashboard');
  if (!el) return;
  const existing = el.querySelector('.dash-alerts');
  const html = renderAlertsHtml(dashboardAlerts);
  if (!html) {
    if (existing) existing.remove();
    return;
  }
  if (existing) {
    existing.outerHTML = html;
  } else {
    const statGrid = el.querySelector('.stat-grid');
    if (statGrid) statGrid.insertAdjacentHTML('beforebegin', html);
  }
  bindDashboardAlerts(el);
  hydrateIcons(el.querySelector('.dash-alerts'));
}

export function bindDashboardAlerts(root) {
  (root || document).querySelectorAll('.dash-alert-dismiss').forEach(function (btn) {
    if (btn.dataset.alertBound) return;
    btn.dataset.alertBound = '1';
    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      const key = btn.getAttribute('data-alert-key');
      if (key) dismissAlertKey(key);
      const row = btn.closest('.dash-alert');
      if (row) row.remove();
      const wrap = (root || document).querySelector('.dash-alerts');
      if (wrap && !wrap.querySelector('.dash-alert')) wrap.remove();
    });
  });
}
