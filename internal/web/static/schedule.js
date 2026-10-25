import { ensureQbitCategories, fetchJSON, qbitCategoriesCache, qbitCategoriesUnavailable, showToast } from './api.js';
import { initChipInput, initHintToggles, initSegmentControls, setSaving } from './components.js';
import { confirmDialog } from './confirm.js';
import { hydrateIcons } from './icons.js';
import { advancedEnabled, applyAdvanced, applyTheme, THEMES } from './layout.js';
import { clearOnboardingDismiss } from './onboarding.js';
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
    webCard = html`<div class="card" id="web-access-card"><div class="card-header"><h2 class="card-title">${t('settings.webAccess')}</h2></div><div class="card-body"><div class="form-group"><label class="form-label" for="allowed_hosts-field">${t('settings.allowedHosts')}</label><div class="chip-input" id="allowed_hosts"><div class="chip-input-chips"></div><input type="text" id="allowed_hosts-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="allowed_hosts-hints"></div><p class="form-hint">${t('settings.allowedHostsHint')}</p></div><div class="form-group"><label class="form-label" for="app_url">${t('settings.appUrl')}</label><input type="url" class="input" id="app_url" autocomplete="off" placeholder="https://" value="${web.app_url || ''}"><p class="form-hint">${t('settings.appUrlHint')}</p></div>${portGroup}</div></div>`;
  }
  const qbitNote = qbitDown
    ? html`<p class="form-hint form-hint-warn">${t('settings.qbitUnavailable')}</p>`
    : '';
  const tagsSupported = sch.tags_supported !== false;
  // у Deluge без сохраненного режима tags только выведен из тега по умолчанию: не закрепляем его первым сохранением
  let watchMode = sch.watch_mode === 'tags' && (tagsSupported || sch.watch_mode_stored) ? 'tags' : 'categories';
  const shownMode = tagsSupported ? watchMode : 'categories';
  const tagsNote = tagsSupported ? '' : html`<p class="form-hint form-hint-keep">${t('settings.tagsUnsupported')}</p>`;
  const modeHint = function (mode) {
    return t(mode === 'tags' ? 'settings.watchTagsHint' : 'settings.categoriesHint');
  };
  const modeButton = function (value, key, enabled) {
    const on = shownMode === value;
    return html`<button type="button" class="seg-ctrl-btn${on ? ' active' : ''}" data-value="${value}" aria-pressed="${on ? 'true' : 'false'}"${enabled ? '' : ' disabled'}>${t(key)}</button>`;
  };

  el.innerHTML = html`<div class="page-header"><div class="page-header-row int-page-header"><h1 class="page-title">${t('settings.title')}</h1><div class="page-header-actions form-submit-row"><button type="button" class="btn btn-primary" id="settings-save" disabled>${t('common.save')}</button><p class="form-hint">${t('settings.saveHint')}</p></div></div></div><div class="stack"><form id="schedule-form" class="stack"><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.interface')}</h2></div><div class="card-body"><div class="form-row"><div class="form-group"><label class="form-label" for="ui_lang">${t('settings.language')}</label><select class="select" id="ui_lang" name="ui_lang"><option value="ru"${getLang() === 'ru' ? ' selected' : ''}>${t('settings.languageRu')}</option><option value="en"${getLang() === 'en' ? ' selected' : ''}>${t('settings.languageEn')}</option></select></div><div class="form-group"><label class="form-label" for="ui_theme">${t('settings.theme')}</label><select class="select" id="ui_theme" name="ui_theme"><option value="dark"${currentTheme() === 'dark' ? ' selected' : ''}>${t('settings.themeDark')}</option><option value="light"${currentTheme() === 'light' ? ' selected' : ''}>${t('settings.themeLight')}</option><option value="oled"${currentTheme() === 'oled' ? ' selected' : ''}>${t('settings.themeOled')}</option></select></div></div><hr class="form-divider"><div class="form-group"><label class="form-switch"><input type="checkbox" id="ui_advanced"${advancedEnabled() ? ' checked' : ''}><span>${t('settings.advanced')}</span></label><p class="form-hint">${t('settings.advancedHint')}</p></div><hr class="form-divider"><div class="form-group form-submit-row"><button type="button" class="btn btn-ghost" id="onboarding-reset">${t('settings.onboardingReset')}</button><p class="form-hint">${t('settings.onboardingResetHint')}</p></div></div></div><div class="card">
<div class="card-header"><h2 class="card-title">${t('settings.schedule')}</h2></div><div class="card-body"><div class="form-row"><div class="form-group"><label class="form-label" for="start_hour">${t('settings.startHour')}</label><select class="select" id="start_hour" name="start_hour">${hourOptions(sch.start_hour)}</select></div><div class="form-group"><label class="form-label" for="list_refresh_per_day">${t('settings.runsPerDay')}</label><select class="select" id="list_refresh_per_day" name="list_refresh_per_day">${refreshOptions(sch.list_refresh_per_day)}</select></div><div class="form-group"><label class="form-label" for="timezone">${t('settings.timezone')}</label><select class="select" id="timezone" name="timezone">${timezoneOptions(timezones, sch.timezone || '')}</select></div></div><div class="settings-advanced"><hr class="form-divider"><p class="form-section-title">${t('settings.thresholds')}</p><div class="form-row"><div class="form-group"><label class="form-label" for="threshold_month">${t('settings.thresholdMonth')}</label><input type="number" class="input" id="threshold_month" min="1" value="${sch.threshold_month}"></div><div class="form-group"><label class="form-label" for="threshold_half_year">${t('settings.thresholdHalfYear')}</label><input type="number" class="input" id="threshold_half_year" min="1" value="${sch.threshold_half_year}"></div><div class="form-group"><label class="form-label" for="threshold_year">${t('settings.thresholdYear')}</label><input type="number" class="input" id="threshold_year" min="1" value="${sch.threshold_year}"></div></div><div class="form-row"><div class="form-group"><label class="form-label" for="error_streak_threshold">${t('settings.errorStreakThreshold')}</label><input type="number" class="input" id="error_streak_threshold" min="1" value="${sch.error_streak_threshold || 3}"></div><div class="form-group"><label class="form-label" for="magnet_timeout">${t('settings.magnetTimeout')}</label><input type="number" class="input" id="magnet_timeout" min="5" max="600" value="${sch.magnet_timeout ?? 60}"></div></div><p class="form-hint">${t('settings.magnetTimeoutHint')}</p><hr class="form-divider"><div class="form-group"><label class="form-switch"><input type="checkbox" id="run_on_start"${sch.run_on_start !== false ? ' checked' : ''}><span>${t('settings.runOnStart')}</span></label><p class="form-hint">${t('settings.runOnStartHint')}</p></div></div></div></div><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.tracking')}</h2></div><div class="card-body"><div class="form-group"><span class="form-label" id="watch-mode-label">${t('settings.watchMode')}</span><div class="seg-ctrl watch-mode" id="watch-mode" role="group" aria-labelledby="watch-mode-label"><div class="seg-ctrl-thumb" aria-hidden="true"></div>${modeButton('categories', 'settings.watchModeCategories', true)}${modeButton('tags', 'settings.watchModeTags', tagsSupported)}</div><p class="form-hint watch-mode-hint">${modeHint(shownMode)}</p>${tagsNote}</div><div class="watch-lists"><div class="form-group${shownMode === 'categories' ? '' : ' hidden'}" id="watch-categories-group"><div class="chip-input" id="monitored_categories"><div class="chip-input-chips"></div><input type="text" id="monitored_categories-field" class="chip-input-field" autocomplete="off" aria-label="${t('settings.categories')}" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="monitored_categories-hints"></div>${qbitNote}</div><div class="form-group${shownMode === 'tags' ? '' : ' hidden'}" id="watch-tags-group"><div class="chip-input" id="watch_tags"><div class="chip-input-chips"></div><input type="text" id="watch_tags-field" class="chip-input-field" autocomplete="off" aria-label="${t('settings.watchTags')}" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="watch_tags-hints"></div></div></div></div></div><div class="card card-danger"><div class="card-header"><span class="card-danger-icon" data-icon="warning"></span><h2 class="card-title">${t('settings.dangerZone')}</h2></div><div class="card-body"><p class="form-hint card-danger-hint">${t('settings.dangerZoneHint')}</p><div class="form-group"><label class="form-switch"><input type="checkbox" id="delete_enabled"${sch.delete_enabled ? ' checked' : ''}><span>${t('settings.deleteEnabled')}</span></label><p class="form-hint">${t('settings.deleteEnabledHint')}</p></div><div id="delete-dependent"><div class="form-group"><label class="form-switch"><input type="checkbox" id="delete_files_enabled"${sch.delete_files_enabled ? ' checked' : ''}><span>${t('settings.deleteFiles')}</span></label><p class="form-hint">${t('settings.deleteFilesHint')}</p></div><div class="form-group" id="delete-files-categories-group"><label class="form-label" for="delete_files_categories-field">${t('settings.deleteCategories')}</label><div class="chip-input" id="delete_files_categories"><div class="chip-input-chips"></div><input type="text" id="delete_files_categories-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="delete_files_categories-hints"></div><p class="form-hint">${t('settings.deleteCategoriesHint')}</p></div></div></div></div></form>${webCard}${passwordCardHtml()}</div>`;
  initHintToggles(el);
  hydrateIcons(el.querySelector('.card-danger'));

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

  const onboardingReset = el.querySelector('#onboarding-reset');
  onboardingReset.addEventListener('click', async function () {
    onboardingReset.disabled = true;
    try {
      await fetchJSON('/api/onboarding', { method: 'PUT', body: JSON.stringify({ checklist_hidden: false, tour_done: false, replay: true }) });
      clearOnboardingDismiss();
      showToast(t('settings.onboardingResetDone'), 'success');
    } catch (err) {
      showToast(err.message, 'error');
    } finally {
      onboardingReset.disabled = false;
    }
  });

  const monitoredInput = initChipInput(
    el.querySelector('#monitored_categories'),
    el.querySelector('#monitored_categories-hints'),
    sch.monitored_categories || [],
    suggestions,
    updateSave
  );

  const watchTagsInput = initChipInput(
    el.querySelector('#watch_tags'),
    el.querySelector('#watch_tags-hints'),
    sch.watch_tags || [],
    [],
    updateSave
  );

  const deleteInput = initChipInput(
    el.querySelector('#delete_files_categories'),
    el.querySelector('#delete_files_categories-hints'),
    sch.delete_files_categories || [],
    suggestions,
    updateSave
  );

  initSegmentControls(el);
  el.querySelector('#watch-mode').addEventListener('segchange', function (e) {
    watchMode = e.detail.value === 'tags' ? 'tags' : 'categories';
    el.querySelector('#watch-categories-group').classList.toggle('hidden', watchMode !== 'categories');
    el.querySelector('#watch-tags-group').classList.toggle('hidden', watchMode !== 'tags');
    el.querySelector('.watch-mode-hint').textContent = modeHint(watchMode);
    updateSave();
  });

  bindDeleteCascade(el);
  initCustomSelects(el);

  const scheduleForm = el.querySelector('#schedule-form');
  const saveBtn = el.querySelector('#settings-save');
  let saving = false;
  let parts = [];

  function readScheduleBody() {
    const body = {
      start_hour: parseInt(document.getElementById('start_hour').value, 10),
      list_refresh_per_day: parseInt(document.getElementById('list_refresh_per_day').value, 10),
      threshold_month: parseInt(document.getElementById('threshold_month').value, 10),
      threshold_half_year: parseInt(document.getElementById('threshold_half_year').value, 10),
      threshold_year: parseInt(document.getElementById('threshold_year').value, 10),
      error_streak_threshold: parseInt(document.getElementById('error_streak_threshold').value, 10),
      magnet_timeout: parseInt(document.getElementById('magnet_timeout').value, 10),
      run_on_start: document.getElementById('run_on_start').checked,
      delete_enabled: document.getElementById('delete_enabled').checked,
      delete_files_enabled: document.getElementById('delete_files_enabled').checked,
      monitored_categories: monitoredInput.getValues(),
      delete_files_categories: deleteInput.getValues(),
      watch_tags: watchTagsInput.getValues(),
      watch_mode: watchMode,
      timezone: document.getElementById('timezone').value,
    };
    return body;
  }

  // снимок берется после каскада и кастомных селектов: значения, которые форма показывает сразу, считаются исходными
  let scheduleSnap = JSON.stringify(readScheduleBody());

  const schedulePart = {
    // набранный, но не добавленный текст чипа тоже изменение: по blur он станет чипом
    dirty: function () {
      return JSON.stringify(readScheduleBody()) !== scheduleSnap || chipFieldPending(scheduleForm);
    },
    save: async function () {
      const body = readScheduleBody();
      // снимок это отправленное тело: правки во время запроса остаются несохраненными, при ошибке старый снимок
      const sent = JSON.stringify(body);
      await fetchJSON('/api/schedule', { method: 'PUT', body: sent });
      scheduleSnap = sent;
      return t('settings.saved');
    },
  };
  const webPart = web && !web.error ? bindWebCard(el, web, updateSave) : null;
  parts = webPart ? [schedulePart, webPart] : [schedulePart];

  function updateSave() {
    setSaving(saveBtn, saving);
    saveBtn.disabled = saving || !parts.some(function (p) { return p.dirty(); });
  }

  // доступ идет последним: смена порта может увести страницу на новый адрес
  async function saveAll() {
    if (saving) return;
    const dirty = parts.filter(function (p) { return p.dirty(); });
    if (dirty.length === 0) return;
    for (const p of dirty) {
      if (p.confirm && !(await p.confirm())) return;
    }
    saving = true;
    updateSave();
    const done = [];
    try {
      for (const p of dirty) {
        const msg = await p.save();
        if (msg === null) return;
        done.push(msg);
      }
      showToast(done.length > 1 ? t('settings.saved') : done[0], 'success');
    } catch (err) {
      showToast(err.message, 'error');
    } finally {
      saving = false;
      updateSave();
    }
  }

  updateSave();
  scheduleForm.addEventListener('input', updateSave);
  scheduleForm.addEventListener('change', updateSave);
  scheduleForm.addEventListener('submit', function (e) {
    e.preventDefault();
    saveAll();
  });
  saveBtn.addEventListener('click', saveAll);

  bindPasswordCard(el);
}

function bindWebCard(el, web, updateSave) {
  const hostsInput = initChipInput(
    el.querySelector('#allowed_hosts'),
    el.querySelector('#allowed_hosts-hints'),
    web.allowed_hosts || [],
    [],
    updateSave
  );
  // порта в форме нет, когда он задан образом (port_locked): тогда его и не шлем
  const portField = el.querySelector('#web_port');
  const appUrlField = el.querySelector('#app_url');
  const webCard = el.querySelector('#web-access-card');
  let loadedPort = web.port;
  let loadedHosts = (web.allowed_hosts || []).slice();
  let loadedAppUrl = web.app_url || '';

  function webChanges() {
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
    if (portField && portField.value !== '' && port !== loadedPort) body.port = port;
    return body;
  }

  webCard.addEventListener('input', updateSave);
  webCard.addEventListener('change', updateSave);

  return {
    // набранный, но не добавленный домен тоже изменение: по blur он станет чипом
    dirty: function () {
      return Object.keys(webChanges()).length > 0 || chipFieldPending(webCard);
    },
    confirm: async function () {
      const body = webChanges();
      if (!('port' in body)) return true;
      return confirmDialog({ text: t('settings.webPortConfirm', { port: body.port }), confirmLabel: t('settings.webPortApply') });
    },
    save: async function () {
      const body = webChanges();
      if (Object.keys(body).length === 0) return t('settings.webSaved');
      const res = await fetchJSON('/api/web', { method: 'PUT', body: JSON.stringify(body) });
      if (body.allowed_hosts) loadedHosts = body.allowed_hosts;
      if ('app_url' in body) loadedAppUrl = body.app_url;
      // сервер ответил реальным портом: без этого устаревший loadedPort держал бы кнопку включенной
      if ('port' in body && res && !res.moved) loadedPort = res.port;
      if (res && res.moved) {
        if (location.port === String(loadedPort)) {
          location.href = location.protocol + '//' + location.hostname + ':' + res.port + location.pathname + location.search + location.hash;
          return null;
        }
        // порт в адресной строке другой или по умолчанию: открыто через прокси, новый адрес не угадать
        loadedPort = res.port;
        return t('settings.webPortMoved', { port: res.port });
      }
      return t('settings.webSaved');
    },
  };
}

function chipFieldPending(root) {
  return Array.from(root.querySelectorAll('.chip-input-field')).some(function (f) { return f.value.trim() !== ''; });
}


