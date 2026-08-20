import { ensureQbitCategories, fetchJSON, qbitCategoriesCache, qbitCategoriesUnavailable, showToast } from './api.js';
import { initChipInput } from './components.js';
import { initCustomSelects } from './select.js';
import { html, hourOptions, refreshOptions, timezoneOptions } from './util.js';

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
    const sch = await fetchJSON('/api/schedule');
    renderSchedule(el, sch, qbitCategoriesCache || [], qbitCategoriesUnavailable, timezoneEntries());
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export function renderSchedule(el, sch, suggestions, qbitDown, timezones) {
  const qbitNote = qbitDown
    ? html`<p class="form-hint form-hint-warn">${t('settings.qbitUnavailable')}</p>`
    : '';

  el.innerHTML = html`<div class="page-header"><h1 class="page-title">${t('settings.title')}</h1></div><form id="schedule-form" class="stack"><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.interface')}</h2></div><div class="card-body"><div class="form-group"><label class="form-label" for="ui_lang">${t('settings.language')}</label><select class="select" id="ui_lang" name="ui_lang"><option value="ru"${getLang() === 'ru' ? ' selected' : ''}>${t('settings.languageRu')}</option><option value="en"${getLang() === 'en' ? ' selected' : ''}>${t('settings.languageEn')}</option></select></div></div></div><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.schedule')}</h2></div><div class="card-body"><div class="form-row"><div class="form-group"><label class="form-label" for="start_hour">${t('settings.startHour')}</label><select class="select" id="start_hour" name="start_hour">${hourOptions(sch.start_hour)}</select></div><div class="form-group"><label class="form-label" for="list_refresh_per_day">${t('settings.runsPerDay')}</label><select class="select" id="list_refresh_per_day" name="list_refresh_per_day">${refreshOptions(sch.list_refresh_per_day)}</select></div><div class="form-group"><label class="form-label" for="timezone">${t('settings.timezone')}</label><select class="select" id="timezone" name="timezone">${timezoneOptions(timezones, sch.timezone || '')}</select></div></div><hr class="form-divider"><p class="form-section-title">${t('settings.thresholds')}</p><div class="form-row"><div class="form-group"><label class="form-label" for="threshold_month">${t('settings.thresholdMonth')}</label><input type="number" class="input" id="threshold_month" min="1" value="${sch.threshold_month}"></div><div class="form-group"><label class="form-label" for="threshold_half_year">${t('settings.thresholdHalfYear')}</label><input type="number" class="input" id="threshold_half_year" min="1" value="${sch.threshold_half_year}"></div><div class="form-group"><label class="form-label" for="threshold_year">${t('settings.thresholdYear')}</label><input type="number" class="input" id="threshold_year" min="1" value="${sch.threshold_year}"></div><div class="form-group"><label class="form-label" for="error_streak_threshold">${t('settings.errorStreakThreshold')}</label><input type="number" class="input" id="error_streak_threshold" min="1" value="${sch.error_streak_threshold || 3}"></div></div><hr class="form-divider"><div class="form-group"><label class="form-label" for="watch_tags-field">${t('settings.watchTags')}</label><div class="chip-input" id="watch_tags"><div class="chip-input-chips"></div><input type="text" id="watch_tags-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="watch_tags-hints"></div><p class="form-hint">${t('settings.watchTagsHint')}</p></div></div></div><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.categories')}</h2></div><div class="card-body"><div class="form-group"><div class="chip-input" id="monitored_categories"><div class="chip-input-chips"></div><input type="text" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="monitored_categories-hints"></div><p class="form-hint">${t('settings.categoriesHint')}</p>${qbitNote}</div></div></div><div class="card"><div class="card-header"><h2 class="card-title">${t('settings.deletion')}</h2></div><div class="card-body"><div class="form-group"><label class="form-switch"><input type="checkbox" id="delete_enabled"${sch.delete_enabled ? ' checked' : ''}><span>${t('settings.deleteEnabled')}</span></label><p class="form-hint">${t('settings.deleteEnabledHint')}</p></div><div id="delete-dependent"><div class="form-group"><label class="form-switch"><input type="checkbox" id="delete_files_enabled"${sch.delete_files_enabled ? ' checked' : ''}><span>${t('settings.deleteFiles')}</span></label><p class="form-hint">${t('settings.deleteFilesHint')}</p></div><div class="form-group" id="delete-files-categories-group"><label class="form-label" for="delete_files_categories-field">${t('settings.deleteCategories')}</label><div class="chip-input" id="delete_files_categories"><div class="chip-input-chips"></div><input type="text" id="delete_files_categories-field" class="chip-input-field" autocomplete="off" placeholder="${t('common.chipPlaceholder')}"></div><div class="chip-input-hints" id="delete_files_categories-hints"></div><p class="form-hint">${t('settings.deleteCategoriesHint')}</p></div></div></div></div><div class="form-submit-row"><button type="submit" class="btn btn-primary">${t('common.save')}</button><p class="form-hint" style="margin-top:12px">${t('settings.saveHint')}</p></div></form>`;

  const langSelect = el.querySelector('#ui_lang');
  langSelect.addEventListener('change', function () {
    if (langSelect.value !== getLang()) setLang(langSelect.value);
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
      delete_enabled: document.getElementById('delete_enabled').checked,
      delete_files_enabled: document.getElementById('delete_files_enabled').checked,
      monitored_categories: monitoredInput.getValues(),
      delete_files_categories: deleteInput.getValues(),
      watch_tags: watchTagsInput.getValues(),
      timezone: document.getElementById('timezone').value,
    };
    try {
      await fetchJSON('/api/schedule', { method: 'PUT', body: JSON.stringify(body) });
      showToast(t('settings.saved'), 'success');
    } catch (err) {
      showToast(err.message, 'error');
    }
  });
}


