import { fetchJSON, showToast } from './api.js';
import { authState, gotoLogin } from './auth.js';
import { initPasswordToggles, setSaving } from './components.js';
import { hydrateIcons } from './icons.js';
import { html } from './util.js';

const ERROR_KEYS = {
  'invalid current password': 'settings.passwordWrong',
  'password must be at least 8 chars': 'settings.passwordShort',
  'too many attempts, try later': 'settings.passwordTooMany',
  'too many login attempts at once, try later': 'settings.passwordTooMany',
};

function fieldHtml(id, label, autocomplete) {
  return html`<div class="form-group"><label class="form-label" for="${id}">${label}</label><div class="input-password-wrap"><input type="password" class="input" id="${id}" name="${id}" autocomplete="${autocomplete}" required><button type="button" class="input-password-toggle" aria-label="${t('trackers.showPassword')}"><span data-icon="eyeHidden"></span></button></div></div>`;
}

export function passwordCardHtml() {
  // authState бывает еще не загружен при первой отрисовке, тогда имя пустое
  const username = authState && authState.username ? authState.username : '';
  return html`<div class="card" id="password-card"><div class="card-header"><h2 class="card-title">${t('settings.password')}</h2></div><form class="card-body" id="password-form"><input type="text" name="username" autocomplete="username" value="${username}" hidden><div class="form-row password-current-row">${fieldHtml('password_current', t('settings.passwordCurrent'), 'current-password')}</div><div class="form-row">${fieldHtml('password_new', t('settings.passwordNew'), 'new-password')}${fieldHtml('password_repeat', t('settings.passwordRepeat'), 'new-password')}</div><p class="form-hint">${t('settings.passwordHint')}</p><button type="submit" class="btn btn-primary">${t('settings.passwordChange')}</button></form></div>`;
}

export function bindPasswordCard(el) {
  const card = el.querySelector('#password-card');
  if (!card) return;
  hydrateIcons(card);
  initPasswordToggles(card);

  const form = card.querySelector('#password-form');
  const submit = form.querySelector('button[type="submit"]');
  let passwordSaving = false;

  function updatePasswordSubmit() {
    setSaving(submit, passwordSaving);
    // без trim: пробел допустимый символ пароля; длину проверяет сервер
    const filled = ['password_current', 'password_new', 'password_repeat'].every(function (name) {
      return form.elements[name].value !== '';
    });
    submit.disabled = passwordSaving || !filled;
  }

  updatePasswordSubmit();
  form.addEventListener('input', updatePasswordSubmit);
  // change ловит автозаполнение менеджером паролей без события input
  form.addEventListener('change', updatePasswordSubmit);
  form.addEventListener('submit', async function (e) {
    e.preventDefault();
    const current = form.elements.password_current.value;
    const next = form.elements.password_new.value;
    if (next !== form.elements.password_repeat.value) {
      showToast(t('settings.passwordMismatch'), 'error');
      return;
    }
    passwordSaving = true;
    updatePasswordSubmit();
    try {
      await fetchJSON('/api/auth/password', { method: 'POST', body: JSON.stringify({ current: current, new: next }) });
      form.reset();
      showToast(t('settings.passwordChanged'), 'success');
    } catch (err) {
      if (err.message === 'unauthorized' || err.message === 'account setup required') {
        gotoLogin();
        return;
      }
      showToast(ERROR_KEYS[err.message] ? t(ERROR_KEYS[err.message]) : err.message, 'error');
    } finally {
      // form.reset() не шлет событий, поэтому кнопку гасит этот вызов
      passwordSaving = false;
      updatePasswordSubmit();
    }
  });
}
