import { ensureQbitCategories, fetchJSON, qbitCategoriesCache, qbitCategoriesUnavailable, showToast } from './api.js';
import { initChipInput, initHintToggles, setSaving } from './components.js';
import { confirmDialog } from './confirm.js';
import { advancedEnabled, applyAdvanced, applyTheme, THEMES } from './layout.js';
import { bindPasswordCard, passwordCardHtml } from './password.js';
import { initCustomSelects } from './select.js';
import { html, hourOptions, refreshOptions, timezoneOptions } from './util.js';

function currentTheme() {
  const theme = document.documentElement.getAttribute('data-theme');
  return THEMES.includes(theme) ? theme : 'dark';
}

export function bindDeleteCascade(el) {
  const deleteEnabled = el.querySelector('#delete_enabled');
  const deleteFilesEnabled = el.querySelector('#delete_files_enabled');
  const deleteSection = el.querySelector('#delete-dependent');
  const deleteFilesGroup = el.querySelector('#delete-files-categories-group');

  function update() {
    const delOn = deleteEnabled.checked;
    deleteFilesEnabled.disabled = !delOn;
    if (deleteSection) deleteSection.classList.toggle('card-disabled', !delOn);
    if (!delOn) deleteFilesEnabled.checked = false;

    const filesOn = delOn && deleteFilesEnabled.checked;
    if (deleteFilesGroup) {
      deleteFilesGroup.style.display = filesOn ? '' : 'none';
    }
  }

  deleteEnabled.addEventListener('change', update);
  deleteFilesEnabled.addEventListener('change', update);
  update();
}

export async function loadSchedule() {
  const el = document.getElementById('view-schedule');
  el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
  try {
    await ensureQbitCategories();
    // сбой /api/web не должен ломать форму расписания: карточка покажет ошибку сама
    const [sch, web] = await Promise.all([
      fetchJSON('/api/schedule'),
      fetchJSON('/api/web').catch(function (err) { return { error: err.message }; }),
    ]);
    renderSchedule(el, sch, qbitCategoriesCache || [], qbitCategoriesUnavailable, timezoneEntries(sch.timezone || ''), web);
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export function renderSchedule(el, sch, suggestions, qbitDown, timezones, web) {
  let webCard = '';
  if (web && web.error) {
    webCard = html`<div class="card"><div class="card-header"><h2 class="card-title">${t('settings.webAccess')}</h2></div><div class="card-body"><div class="alert alert-danger">${web.error}</div></div></div>`;
  } else if (web) {
    // в Docker порт задан образом, поле только запутало бы: менять надо левую часть -p
    const portGroup = web.port_locked
      ? ''
      : html`<div class="form-group"><label class="form-label" for="web_port">${t('settings.webPort')}</label><input type="number" class="input" id="web_port" min="1" max="65535" value="${web.port}"><p class="form-hint">${t('settings.webPortHint')}</p></div>`;
    webCard = html`<div class="card" id="web-access-card"><div class="card-header"><h2 class="card-title">${t('settings.webAccess')}</h2></div><div class="card-body"><div class="form-group"><label class="form-label" for="allowed_hosts-field">${t('settings.allowedHosts')}</label><div class="chip-input" id="allowed_hosts"><div class="chip-input-chips"></div><input type="text" id="allowed_hosts-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="allowed_hosts-hints"></div><p class="form-hint">${t('settings.allowedHostsHint')}</p></div><div class="form-group"><label class="form-label" for="app_url">${t('settings.appUrl')}</label><input type="url" class="input" id="app_url" autocomplete="off" placeholder="https://" value="${web.app_url || ''}"><p class="form-hint">${t('settings.appUrlHint')}</p></div>${portGroup}<button type="button" class="btn btn-primary" id="web-save">${t('common.save')}</button></div></div>`;
  }
  const qbitNote = qbitDown
    ? html`<p class="form-hint form-hint-warn">${t('settings.qbitUnavailable')}</p>`
    : '';
  // старый сервер не знает mirror_confirm: переключатель без действия только запутал бы
  const mirrorSection = typeof sch.mirror_confirm === 'boolean'
    ? html`<hr class="form-divider"><p class="form-section-title">${t('settings.mirrorsSection')}</p><div class="form-group"><label class="form-switch"><input type="checkbox" id="mirror_confirm"${sch.mirror_confirm ? ' checked' : ''}><span>${t('settings.mirrorConfirm')}</span></label><p class="form-hint">${t('settings.mirrorConfirmHint')}</p></div>`
    : '';

  el.innerHTML = html`<div class="page-header"><h1 class="page-title">${t('settings.title')}</h1></div><div class="stack"><form id="schedule-form" class="stack"><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.interface')}</h2></div><div class="card-body"><div class="form-row"><div class="form-group"><label class="form-label" for="ui_lang">${t('settings.language')}</label><select class="select" id="ui_lang" name="ui_lang"><option value="ru"${getLang() === 'ru' ? ' selected' : ''}>${t('settings.languageRu')}</option><option value="en"${getLang() === 'en' ? ' selected' : ''}>${t('settings.languageEn')}</option></select></div><div class="form-group"><label class="form-label" for="ui_theme">${t('settings.theme')}</label><select class="select" id="ui_theme" name="ui_theme"><option value="dark"${currentTheme() === 'dark' ? ' selected' : ''}>${t('settings.themeDark')}</option><option value="light"${currentTheme() === 'light' ? ' selected' : ''}>${t('settings.themeLight')}</option><option value="oled"${currentTheme() === 'oled' ? ' selected' : ''}>${t('settings.themeOled')}</option></select></div></div><hr class="form-divider"><div class="form-group"><label class="form-switch"><input type="checkbox" id="ui_advanced"${advancedEnabled() ? ' checked' : ''}><span>${t('settings.advanced')}</span></label></div></div></div><div class="card">
<div class="card-header"><h2 class="card-title">${t('settings.schedule')}</h2></div><div class="card-body"><div class="form-row"><div class="form-group"><label class="form-label" for="start_hour">${t('settings.startHour')}</label><select class="select" id="start_hour" name="start_hour">${hourOptions(sch.start_hour)}</select></div><div class="form-group"><label class="form-label" for="list_refresh_per_day">${t('settings.runsPerDay')}</label><select class="select" id="list_refresh_per_day" name="list_refresh_per_day">${refreshOptions(sch.list_refresh_per_day)}</select></div><div class="form-group"><label class="form-label" for="timezone">${t('settings.timezone')}</label><select class="select" id="timezone" name="timezone">${timezoneOptions(timezones, sch.timezone || '')}</select></div></div><hr class="form-divider"><p class="form-section-title">${t('settings.thresholds')}</p><div class="form-row"><div class="form-group"><label class="form-label" for="threshold_month">${t('settings.thresholdMonth')}</label><input type="number" class="input" id="threshold_month" min="1" value="${sch.threshold_month}"></div><div class="form-group"><label class="form-label" for="threshold_half_year">${t('settings.thresholdHalfYear')}</label><input type="number" class="input" id="threshold_half_year" min="1" value="${sch.threshold_half_year}"></div><div class="form-group"><label class="form-label" for="threshold_year">${t('settings.thresholdYear')}</label><input type="number" class="input" id="threshold_year" min="1" value="${sch.threshold_year}"></div></div><div class="form-row"><div class="form-group"><label class="form-label" for="error_streak_threshold">${t('settings.errorStreakThreshold')}</label><input type="number" class="input" id="error_streak_threshold" min="1" value="${sch.error_streak_threshold || 3}"></div><div class="form-group"><label class="form-label" for="magnet_timeout">${t('settings.magnetTimeout')}</label><input type="number" class="input" id="magnet_timeout" min="5" max="600" value="${sch.magnet_timeout ?? 60}"></div></div><p class="form-hint">${t('settings.magnetTimeoutHint')}</p><hr class="form-divider"><div class="form-group"><label class="form-label form-section-title" for="watch_tags-field">${t('settings.watchTags')}</label><div class="chip-input" id="watch_tags"><div class="chip-input-chips"></div><input type="text" id="watch_tags-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="watch_tags-hints"></div><p class="form-hint">${t('settings.watchTagsHint')}</p></div><hr class="form-divider"><p class="form-section-title">${t('settings.categories')}</p><div class="form-group"><div class="chip-input" id="monitored_categories"><div class="chip-input-chips"></div><input type="text" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="monitored_categories-hints"></div><p class="form-hint">${t('settings.categoriesHint')}</p>${qbitNote}</div><hr class="form-divider"><p class="form-section-title">${t('settings.deletion')}</p><div class="form-group"><label class="form-switch"><input type="checkbox" id="delete_enabled"${sch.delete_enabled ? ' checked' : ''}><span>${t('settings.deleteEnabled')}</span></label><p class="form-hint">${t('settings.deleteEnabledHint')}</p></div><div id="delete-dependent"><div class="form-group"><label class="form-switch"><input type="checkbox" id="delete_files_enabled"${sch.delete_files_enabled ? ' checked' : ''}><span>${t('settings.deleteFiles')}</span></label><p class="form-hint">${t('settings.deleteFilesHint')}</p></div><div class="form-group" id="delete-files-categories-group"><label class="form-label" for="delete_files_categories-field">${t('settings.deleteCategories')}</label><div class="chip-input" id="delete_files_categories"><div class="chip-input-chips"></div><input type="text" id="delete_files_categories-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="delete_files_categories-hints"></div><p class="form-hint">${t('settings.deleteCategoriesHint')}</p></div></div>${mirrorSection}<div class="form-submit-row"><button type="submit" class="btn btn-primary">${t('common.save')}</button><p class="form-hint">${t('settings.saveHint')}</p></div></div></div></form>${webCard}${passwordCardHtml()}</div>`;
  initHintToggles(el);

  const langSelect = el.querySelector('#ui_lang');
  langSelect.addEventListener('change', function () {
    if (langSelect.value !== getLang()) setLang(langSelect.value);
  });

  const themeSelect = el.querySelector('#ui_theme');
  themeSelect.addEventListener('change', function () {
    applyTheme(themeSelect.value);
  });

  const advancedToggle = el.querySelector('#ui_advanced');
  advancedToggle.addEventListener('change', function () {
    applyAdvanced(advancedToggle.checked);
  });

  const monitoredInput = initChipInput(
    el.querySelector('#monitored_categories'),
    el.querySelector('#monitored_categories-hints'),
    sch.monitored_categories || [],
    suggestions
  );

  const watchTagsInput = initChipInput(
    el.querySelector('#watch_tags'),
    el.querySelector('#watch_tags-hints'),
    sch.watch_tags || [],
    []
  );

  const deleteInput = initChipInput(
    el.querySelector('#delete_files_categories'),
    el.querySelector('#delete_files_categories-hints'),
    sch.delete_files_categories || [],
    suggestions
  );

  bindDeleteCascade(el);
  initCustomSelects(el);

  el.querySelector('#schedule-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    const body = {
      start_hour: parseInt(document.getElementById('start_hour').value, 10),
      list_refresh_per_day: parseInt(document.getElementById('list_refresh_per_day').value, 10),
      threshold_month: parseInt(document.getElementById('threshold_month').value, 10),
      threshold_half_year: parseInt(document.getElementById('threshold_half_year').value, 10),
      threshold_year: parseInt(document.getElementById('threshold_year').value, 10),
      error_streak_threshold: parseInt(document.getElementById('error_streak_threshold').value, 10),
      magnet_timeout: parseInt(document.getElementById('magnet_timeout').value, 10),
      delete_enabled: document.getElementById('delete_enabled').checked,
      delete_files_enabled: document.getElementById('delete_files_enabled').checked,
      monitored_categories: monitoredInput.getValues(),
      delete_files_categories: deleteInput.getValues(),
      watch_tags: watchTagsInput.getValues(),
      timezone: document.getElementById('timezone').value,
    };
    const mirrorConfirm = document.getElementById('mirror_confirm');
    if (mirrorConfirm) body.mirror_confirm = mirrorConfirm.checked;
    const submitBtn = e.currentTarget.querySelector('button[type="submit"]');
    if (submitBtn && submitBtn.disabled) return;
    setSaving(submitBtn, true);
    try {
      await fetchJSON('/api/schedule', { method: 'PUT', body: JSON.stringify(body) });
      showToast(t('settings.saved'), 'success');
    } catch (err) {
      showToast(err.message, 'error');
    } finally {
      setSaving(submitBtn, false);
    }
  });

  if (web && !web.error) bindWebCard(el, web);
  bindPasswordCard(el);
}

function bindWebCard(el, web) {
  const hostsInput = initChipInput(
    el.querySelector('#allowed_hosts'),
    el.querySelector('#allowed_hosts-hints'),
    web.allowed_hosts || [],
    []
  );
  // порта в форме нет, когда он задан образом (port_locked): тогда его и не шлем
  const portField = el.querySelector('#web_port');
  const appUrlField = el.querySelector('#app_url');
  const saveBtn = el.querySelector('#web-save');
  let loadedPort = web.port;
  let loadedHosts = (web.allowed_hosts || []).slice();
  let loadedAppUrl = web.app_url || '';

  saveBtn.addEventListener('click', async function () {
    const body = {};
    const hosts = hostsInput.getValues();
    // список доменов уходит только при смене, иначе сохранение порта запишет в БД список из env
    if (hosts.length !== loadedHosts.length || hosts.some(function (h, i) { return h !== loadedHosts[i]; })) {
      body.allowed_hosts = hosts;
    }
    const appUrl = appUrlField.value.trim();
    if (appUrl !== loadedAppUrl) body.app_url = appUrl;
    const port = portField ? parseInt(portField.value, 10) : NaN;
    // порт уходит только при реальной смене, иначе сохранение доменов его не трогает
    if (portField && portField.value !== '' && port !== loadedPort) {
      if (!(await confirmDialog({ text: t('settings.webPortConfirm', { port: port }), confirmLabel: t('settings.webPortApply') }))) return;
      body.port = port;
    }
    if (Object.keys(body).length === 0) {
      showToast(t('settings.webSaved'), 'success');
      return;
    }
    setSaving(saveBtn, true);
    try {
      const res = await fetchJSON('/api/web', { method: 'PUT', body: JSON.stringify(body) });
      if (body.allowed_hosts) loadedHosts = body.allowed_hosts;
      if ('app_url' in body) loadedAppUrl = body.app_url;
      if (res && res.moved) {
        if (location.port === String(loadedPort)) {
          location.href = location.protocol + '//' + location.hostname + ':' + res.port + location.pathname + location.search + location.hash;
          return;
        }
        // порт в адресной строке другой или по умолчанию: открыто через прокси, новый адрес не угадать
        loadedPort = res.port;
        showToast(t('settings.webPortMoved', { port: res.port }), 'success');
      } else {
        showToast(t('settings.webSaved'), 'success');
      }
    } catch (err) {
      showToast(err.message, 'error');
    } finally {
      setSaving(saveBtn, false);
    }
  });
}


