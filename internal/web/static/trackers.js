import { fetchJSON, showToast } from './api.js';
import { activeErrorPopover, cancelClose, closeAnimated, closePopovers, initChipInput, initHintToggles, initPasswordToggles, setSaving } from './components.js';
import { confirmDialog } from './confirm.js';
import { hydrateIcons, icon } from './icons.js';
import { integrationsCache, setIntegrationsCache } from './integrations.js';
import { closeOpenCustomSelect, initCustomSelects, openCustomSelect } from './select.js';
import { abortTest, bindTestButton, forgetTest, paintTestButton, paintTestLine, resetTest, runTest } from './testbutton.js';
import { html, randomFieldName, secureInputAttrs, trackerDisplayDomain } from './util.js';

const trackersByKey = new Map();
let dialog = null;
let closingOverlay = null;
let trackerTypes = [];

function domainOptionsHtml(base, hosts, selected) {
  const list = hosts || [];
  const first = list.filter(function (h) { return mirrorHostKey(h) === mirrorHostKey(base); }).slice(0, 1);
  const seen = [];
  const parts = [];
  first.concat(list).forEach(function (h) {
    if (!h || seen.indexOf(mirrorHostKey(h)) !== -1) return;
    seen.push(mirrorHostKey(h));
    parts.push(html`<option value="${h}"${selected === h ? ' selected' : ''}>${h}</option>`);
  });
  return html`${parts}`;
}

function syncTrackerDomainSelects(root, base, hosts) {
  const select = root.querySelector('.tr-domain');
  if (select && hosts && hosts.length) {
    // выбранный основным адрес убрали из списка: берем первый оставшийся
    const next = hosts.indexOf(select.value) === -1 ? hosts[0] : select.value;
    select.innerHTML = domainOptionsHtml(base, hosts, next);
    select.value = next;
  }
  hideDomainChip(root);
}

function hideDomainChip(root) {
  const domain = root.querySelector('.tr-domain')?.value;
  // основной домен в списке зеркал не зеркало. Чип только прячем: выпадающий список доменов
  // строится из этих значений, и выбранный в нем домен пропал бы
  root.querySelectorAll('.tr-mirrors .chip').forEach(function (chip) {
    const label = chip.querySelector('.chip-label');
    chip.classList.toggle('chip-is-domain', !!label && mirrorHostKey(label.textContent) === mirrorHostKey(domain));
  });
}

function syncMirrorsVisibility(root) {
  const sw = root.querySelector('.tr-use-mirrors');
  const field = root.querySelector('.tr-mirrors');
  // без зеркал поле только прячем: чипы остаются и уходят при сохранении.
  // _mirrorsForced: диалог с пометкой на строке показывает поле, не трогая переключатель
  if (sw && field) field.closest('.form-group').hidden = !sw.checked && !root._mirrorsForced;
}

function trackerDomainChoice(tracker, mirrors) {
  // явный активный домен в старом виде: зеркало становится основным, ротация выключена
  const domain = tracker.domain || '';
  const active = tracker.active_domain || '';
  if (!active) return { domain: domain, useMirrors: true };
  const m = (mirrors || []).find(function (x) { return mirrorHostKey(x) === mirrorHostKey(active); });
  return { domain: m && mirrorHostKey(m) !== mirrorHostKey(domain) ? m : domain, useMirrors: false };
}

export function trackerConfigFromApi(tracker) {
  return {
    enabled: !!tracker.enabled,
    domain: tracker.domain || '',
    hour_limit: tracker.hour_limit || 0,
    proxy_type: tracker.proxy_type || 0,
    login_url: tracker.login_url || '',
    download_url: tracker.download_url || '',
    credentials: tracker.credentials || '',
    ...(tracker.supports_login ? { login: tracker.login || '', password: tracker.password || '' } : {}),
    cookies: tracker.cookies || '',
    publicity: tracker.publicity || 'public',
    mirrors: (tracker.mirrors || []).slice(),
    active_domain: tracker.active_domain || '',
  };
}

function trackerLoginValues(item) {
  // у трекеров со входом по кукам полей логина нет. Пустые значения слать нельзя,
  // сохранение затерло бы чужую строку входа
  const login = item.querySelector('.tr-login');
  const pass = item.querySelector('[data-field="password"]');
  if (!login || !pass) return {};
  return { login: login.value, password: pass.value };
}

function trackerTestMissing(cfg) {
  // пустая строка: тест входа можно запускать
  if (cfg.publicity === 'public' || (cfg.cookies || '').trim()) return '';
  if ('login' in cfg) {
    return (cfg.login || '').trim() && cfg.password ? '' : t('trackers.testNeedsLogin');
  }
  return (cfg.credentials || '').trim() ? '' : t('trackers.testNeedsCookies');
}

export function gatherTrackerConfig(root, mirrorValues, enabled) {
  return {
    enabled: !!enabled,
    domain: root.querySelector('.tr-domain').value,
    hour_limit: parseInt(root.querySelector('.tr-hour-limit').value, 10) || 0,
    proxy_type: parseInt(root.querySelector('.tr-proxy-type').value, 10),
    login_url: root.querySelector('.tr-login-url').value,
    download_url: root.querySelector('.tr-download-url').value,
    credentials: root.querySelector('[data-field="credentials"]').value,
    ...trackerLoginValues(root),
    cookies: root.querySelector('.tr-cookies').value,
    publicity: root.querySelector('.tr-publicity').value,
    mirrors: mirrorsForSave(mirrorValues, root.querySelector('.tr-domain').value, root._trackerBase),
    active_domain: root.querySelector('.tr-use-mirrors').checked ? '' : root.querySelector('.tr-domain').value,
  };
}

export function isTrackerDirty(root, mirrorValues) {
  // enabled не сравниваем: переключатель на карточке сохраняется сам по себе,
  // форма диалога им не владеет
  if (!root._trackerSaved) return false;
  const cur = gatherTrackerConfig(root, mirrorValues, root._trackerSaved.enabled);
  return JSON.stringify(cur) !== JSON.stringify(root._trackerSaved);
}

export function hasUnsavedTrackers() {
  // правки живут только в открытом диалоге. Роутер спрашивает тут перед уходом,
  // иначе loadTrackers перерисует список и правки молча пропадут
  return !!dialog && isTrackerDirty(dialog.root, dialog.mirrors.getValues());
}

function trackerRow(inner, cls) {
  // ряды по два поля, без общей сетки: ряд целиком основной или расширенный,
  // и при переключении расширенных в строках не остается дыр
  return html`<div class="form-row form-row-pair${cls ? ' ' + cls : ''}">${inner}</div>`;
}

function trackerFieldHtml(key, name, label, value, extraCls) {
  return html`<div class="form-group"><label class="form-label" for="tr-${name}-${key}">${label}</label><input type="text" id="tr-${name}-${key}" class="input tr-${extraCls || name}" autocomplete="off" name="${randomFieldName('tr-' + name)}" value="${value || ''}"></div>`;
}

function loginFieldsHtml(tracker, key) {
  // логин и пароль только для входа формой, nnm и teamhd пускают по кукам
  if (!tracker.supports_login) return '';
  return trackerRow(html`<div class="form-group"><label class="form-label" for="tr-login-${key}">${t('trackers.login')}</label><input type="text" id="tr-login-${key}" class="input tr-login" autocomplete="off" name="${randomFieldName('tr-user')}" value="${tracker.login || ''}"></div><div class="form-group"><label class="form-label" for="tr-pass-${key}">${t('trackers.password')}</label><div class="input-password-wrap"><input id="tr-pass-${key}" data-field="password" data-reveal="/api/trackers/${encodeURIComponent(key)}/secret" data-reveal-field="password"${secureInputAttrs('tr-pass')} value="${tracker.password || ''}"><button type="button" class="input-password-toggle" aria-label="${t('trackers.showPassword')}"><span data-icon="eyeHidden"></span></button></div></div>`);
}

function trackerFormHtml(tracker, key, pub, mirrors) {
  const choice = trackerDomainChoice(tracker, mirrors);
  const address = trackerRow(html`<div class="form-group"><label class="form-label" for="tr-domain-${key}">${t('trackers.domain')}</label><select class="select tr-domain" id="tr-domain-${key}">${domainOptionsHtml(tracker.domain || '', trackerHosts(tracker.domain, mirrors), choice.domain)}</select><p class="form-hint">${t('trackers.domainMirrorHint')}</p></div><div class="form-group tr-use-mirrors-group"><label class="form-switch"><input type="checkbox" class="tr-use-mirrors"${choice.useMirrors ? ' checked' : ''}><span>${t('trackers.useMirrors')}</span></label><p class="form-hint">${t('trackers.useMirrorsHint')}</p></div>`, 'tr-address-row');

  const mirrorsRow = html`<div class="form-group form-group-full"><label class="form-label" for="tr-mirrors-${key}">${t('trackers.mirrors')}</label><div class="chip-input tr-mirrors"><div class="chip-input-chips"></div><input type="text" id="tr-mirrors-${key}" class="chip-input-field" autocomplete="off" name="${randomFieldName('tr-mirror')}" placeholder="${t('trackers.mirrorsPlaceholder')}"></div></div>`;

  const limits = trackerRow(html`<div class="form-group"><label class="form-label" for="tr-hour-limit-${key}">${t('trackers.hourLimit')}</label><input type="number" id="tr-hour-limit-${key}" class="input tr-hour-limit" min="1" value="${tracker.hour_limit}"></div><div class="form-group"><label class="form-label" for="tr-proxy-type-${key}">${t('trackers.proxy')}</label><select id="tr-proxy-type-${key}" class="select tr-proxy-type"><option value="0"${tracker.proxy_type === 0 ? ' selected' : ''}>${t('trackers.proxyDirect')}</option><option value="1"${tracker.proxy_type === 1 ? ' selected' : ''}>${t('trackers.proxyFlare')}</option></select></div>`);

  const access = trackerRow(html`<div class="form-group"><label class="form-label" for="tr-publicity-${key}">${t('trackers.publicity')}</label><select id="tr-publicity-${key}" class="select tr-publicity"><option value="public"${pub === 'public' ? ' selected' : ''}>${t('trackers.public')}</option><option value="private"${pub === 'private' ? ' selected' : ''}>${t('trackers.private')}</option></select></div>${trackerFieldHtml(key, 'login-url', t('trackers.loginUrl'), tracker.login_url, 'login-url')}`, 'tracker-advanced');

  const downloadURL = html`<div class="form-group form-group-full tracker-advanced"><label class="form-label" for="tr-download-url-${key}">${t('trackers.downloadUrl')}</label><input type="text" id="tr-download-url-${key}" class="input tr-download-url" autocomplete="off" name="${randomFieldName('tr-dl')}" value="${tracker.download_url}"></div>`;

  // приватному трекеру без формы входа (nnm, teamhd) не войти без сырой строки,
  // поэтому она в основных полях
  const rawOnly = !tracker.supports_login && pub === 'private';
  const rawCls = rawOnly ? '' : ' tracker-advanced';
  // у трекеров с формой входа тут шаблон с {login} и {password}, секретов в нем нет:
  // поле открытое. Остальные хранят в теле настоящую строку входа, она остается под маской
  const credInput = tracker.supports_login
    ? html`<input type="text" id="tr-cred-${key}" class="input" data-field="credentials" autocomplete="off" name="${randomFieldName('tr-cred')}" value="${tracker.credentials}">`
    : html`<div class="input-password-wrap"><input id="tr-cred-${key}" data-field="credentials" data-reveal="/api/trackers/${encodeURIComponent(key)}/secret" data-reveal-field="credentials"${secureInputAttrs('tr-cred')} value="${tracker.credentials}"><button type="button" class="input-password-toggle" aria-label="${t('trackers.showCredentials')}"><span data-icon="eyeHidden"></span></button></div>`;
  const credHint = tracker.supports_login ? 'trackers.credentialsHintLogin' : (rawOnly ? 'trackers.credentialsHintRaw' : 'trackers.credentialsHint');
  const raw = html`<div class="form-group form-group-full${rawCls}"><label class="form-label" for="tr-cred-${key}">${t('trackers.credentials')}</label>${credInput}<p class="form-hint">${t(credHint)}</p></div><div class="form-group form-group-full${rawCls}"><label class="form-label" for="tr-cookies-${key}">${t('trackers.cookies')}</label><input type="text" id="tr-cookies-${key}" class="input tr-cookies" autocomplete="off" name="${randomFieldName('tr-cookie')}" value="${tracker.cookies}"></div>`;


  return html`<div class="tracker-form">${address}${mirrorsRow}${limits}${loginFieldsHtml(tracker, key)}${access}${downloadURL}${raw}</div>`;
}


function mirrorHostKey(host) {
  return String(host || '').trim().toLowerCase();
}

function trackerHosts(domain, mirrors) {
  const out = [];
  // основной последним: после смены основного прежний встает в конец ротации, как раньше дописывал сервер
  (mirrors || []).concat([domain]).forEach(function (h) {
    if (h && !hostIn(out, h)) out.push(h);
  });
  return out;
}

function mirrorsForSave(chips, selected, base) {
  // при смене основного сервер принимает новый адрес только из присланных зеркал
  if (mirrorHostKey(selected) !== mirrorHostKey(base)) return (chips || []).slice();
  return (chips || []).filter(function (h) { return mirrorHostKey(h) !== mirrorHostKey(base); });
}

function hostIn(list, host) {
  const k = mirrorHostKey(host);
  return (list || []).some(function (m) { return m && mirrorHostKey(m) === k; });
}

const MIRROR_REASON_KEYS = {
  jackett: 'trackers.suggestReasonJackett',
  dead: 'trackers.suggestReasonDead',
  redirect: 'trackers.suggestReasonRedirect',
  redirect_unlisted: 'trackers.suggestReasonRedirectUnlisted',
};

function mirrorReason(reason) {
  const k = MIRROR_REASON_KEYS[reason];
  return k ? t(k) : '';
}

function mirrorState(tr, chips, domain, active) {
  const confirm = [];
  let paused = '';
  // старый сервер не шлет confirm_on: ни подтверждений, ни паузы
  // выбранное основным или активным сервер считает подтвержденным, такие не предлагаем
  if (tr.confirm_on === true) {
    chips.forEach(function (m) {
      if (!hostIn(tr.unconfirmed, m) || hostIn([domain, active], m)) return;
      if (tr.paused && mirrorHostKey(tr.paused) === mirrorHostKey(m)) paused = m;
      else confirm.push(m);
    });
  }
  const sugs = Array.isArray(tr.suggestions) ? tr.suggestions : [];
  const main = sugs.filter(function (s) {
    return s && s.kind === 'main' && s.host && mirrorHostKey(s.host) !== mirrorHostKey(domain) &&
      (!s.from || mirrorHostKey(s.from) === mirrorHostKey(domain));
  });
  const remove = sugs.filter(function (s) {
    return s && s.kind === 'remove' && hostIn(chips, s.host) && mirrorHostKey(s.host) !== mirrorHostKey(domain);
  });
  const auto = (Array.isArray(tr.auto) ? tr.auto : []).filter(function (a) {
    return a && a.host && (a.kind === 'added' || a.kind === 'removed');
  });
  const pending = !!paused || confirm.length + main.length + remove.length > 0;
  return { paused: paused, confirm: confirm, main: main, remove: remove, auto: auto, pending: pending };
}

function trackerMirrorState(tr) {
  const ms = mirrorState(tr, tr.mirrors || [], tr.domain || '', tr.active_domain || '');
  // явный домен значит выключенные зеркала: изменения списка копятся молча до их включения
  if (tr.active_domain) {
    ms.confirm = [];
    ms.remove = [];
    ms.main = [];
    ms.paused = '';
    ms.pending = false;
  }
  return ms;
}

function rowBadgeTitle(ms) {
  if (ms.paused) return t('trackers.pausedBadge');
  return ms.main.length + ms.remove.length > 0 ? t('trackers.suggestBadge') : t('trackers.mirrorsPendingBadge');
}

function updateRowBadge(key) {
  const tr = trackersByKey.get(key);
  const badge = trackerCard(key)?.querySelector('.tracker-row-badge');
  if (!tr || !badge) return;
  const ms = trackerMirrorState(tr);
  badge.classList.toggle('tracker-row-badge-paused', !!ms.paused);
  badge.title = rowBadgeTitle(ms);
  badge.setAttribute('aria-label', rowBadgeTitle(ms));
  badge.hidden = !ms.pending;
}

const PROXY_LABEL_KEYS = { 1: 'trackers.proxyFlare' };

function proxyChipHtml(tracker) {
  const key = PROXY_LABEL_KEYS[tracker.proxy_type];
  return key ? html`<span class="tracker-row-chip">${t(key)}</span>` : '';
}

function hourLimitText(tracker) {
  // 0 в конфиге значит без лимита: диалог принимает его так же (parseInt || 0)
  return tracker.hour_limit > 0 ? String(tracker.hour_limit) : t('common.emDash');
}

export function trackerItemHtml(tracker) {
  const key = tracker.key;
  const name = tracker.name || key;
  const disabledCls = tracker.enabled ? '' : ' tracker-item-disabled';
  const domain = trackerDisplayDomain(tracker);
  const ms = trackerMirrorState(tracker);
  const badge = html`<span class="tracker-row-badge${ms.paused ? ' tracker-row-badge-paused' : ''}" role="img" title="${rowBadgeTitle(ms)}" aria-label="${rowBadgeTitle(ms)}"${ms.pending ? '' : ' hidden'}></span>`;
  return html`<div class="tracker-item${disabledCls}" data-key="${key}"><div class="tracker-row" role="button" tabindex="0" aria-haspopup="dialog" title="${t('trackers.openSettings')}" aria-label="${name}: ${t('trackers.openSettings')}"><span class="tracker-drag-handle" data-icon="grip" aria-hidden="true" title="${t('trackers.dragHandle')}"></span><label class="form-switch tracker-row-toggle" title="${t('trackers.enabled')}"><input class="tr-enabled" type="checkbox"${tracker.enabled ? ' checked' : ''}><span class="sr-only">${t('trackers.enabled')}</span></label><div class="tracker-row-main"><span class="tracker-row-name">${name}</span><span class="tracker-row-domain"${domain ? '' : ' hidden'}>${domain}</span></div><span class="tracker-row-proxy">${proxyChipHtml(tracker)}</span><span class="tracker-row-limit">${hourLimitText(tracker)}</span>${badge}<button type="button" class="btn btn-ghost btn-icon-only btn-test btn-test-tracker" data-key="${key}" title="${t('trackers.test')}" aria-label="${t('trackers.test')}"><span data-icon="activity"></span></button></div></div>`;
}

export function setTrackerItemEnabled(item, enabled) {
  item.classList.toggle('tracker-item-disabled', !enabled);
}

function trackerCard(key) {
  return document.querySelector('#view-trackers .tracker-item[data-key="' + CSS.escape(key) + '"]');
}

function cardEnabled(key) {
  const sw = trackerCard(key)?.querySelector('.tr-enabled');
  return sw ? sw.checked : false;
}

function updateCardHeader(item, tracker) {
  const domain = trackerDisplayDomain(tracker);
  const el = item.querySelector('.tracker-row-domain');
  if (!el) return;
  el.textContent = domain;
  el.hidden = !domain;
  item.querySelector('.tracker-row-proxy').innerHTML = proxyChipHtml(tracker);
  item.querySelector('.tracker-row-limit').textContent = hourLimitText(tracker);
}

function applyTrackerFilter() {
  const input = document.querySelector('#view-trackers .tracker-filter');
  const list = document.querySelector('#view-trackers .tracker-list');
  if (!input || !list) return;
  // is-filtered выключает перетаскивание: у скрытых карточек нет понятного места в порядке
  const query = input.value.trim().toLocaleLowerCase();
  let shown = 0;
  list.querySelectorAll('.tracker-item').forEach(function (item) {
    const key = item.getAttribute('data-key');
    const tr = trackersByKey.get(key);
    const hay = [tr.name || '', key, trackerDisplayDomain(tr)].join('\n').toLocaleLowerCase();
    const match = query === '' || hay.includes(query);
    item.hidden = !match;
    if (match) shown++;
  });
  list.classList.toggle('is-filtered', query !== '');
  document.querySelector('#view-trackers .tracker-filter-empty').hidden = shown > 0;
}

export function trackerTestTimeoutMs(item) {
  let proxyType = 0;
  if (item) {
    const sel = item.querySelector('.tr-proxy-type');
    if (sel) proxyType = parseInt(sel.value, 10) || 0;
    else if (item._trackerSaved) proxyType = item._trackerSaved.proxy_type || 0;
  }
  if (proxyType !== 1) return 30000;
  const flareMs = integrationsCache && integrationsCache.flaresolverr &&
    integrationsCache.flaresolverr.timeout_ms;
  return flareMs > 0 ? flareMs : 30000;
}

function testEnabled(form, key) {
  // у добавляемого трекера карточки нет, он добавляется включенным
  return form && form._addMode ? true : cardEnabled(key);
}

function testButtonConfig(btn) {
  const root = btn.closest('.modal');
  if (root) {
    return dialog && dialog.root === root
      ? gatherTrackerConfig(root, dialog.mirrors.getValues(), testEnabled(root, dialog.key))
      : null;
  }
  const item = btn.closest('.tracker-item');
  return item && item._trackerSaved;
}

function paintTrackerTestButton(btn) {
  const cfg = testButtonConfig(btn);
  paintTestButton(btn, btn.getAttribute('data-key'), t('trackers.test'), function () {
    return cfg ? trackerTestMissing(cfg) : '';
  });
}

function paintTestButtons(key) {
  document.querySelectorAll('.btn-test-tracker[data-key="' + CSS.escape(key) + '"]').forEach(paintTrackerTestButton);
}

function paintTrackerTest(key) {
  paintTestButtons(key);
  if (dialog && dialog.key === key && dialog.line) paintTestLine(dialog.line, key);
}

function bindTrackerTestButton(btn, onClick) {
  const key = btn.getAttribute('data-key');
  // как раньше: клик по кнопке не должен доходить до строки и документных обработчиков
  bindTestButton(btn, key, onClick, function () { paintTrackerTest(key); });
  btn.addEventListener('click', function (e) { e.stopPropagation(); });
  paintTrackerTestButton(btn);
}

function dialogTestEdit(key) {
  resetTest(key);
  paintTrackerTest(key);
}

export async function testTracker(key, btn, form, mirrorValues) {
  if (!btn) return;
  await runTest(key, async function (signal) {
    const opts = { method: 'POST', signal: signal };
    if (form && (form._addMode || isTrackerDirty(form, mirrorValues))) {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body = JSON.stringify({ config: gatherTrackerConfig(form, mirrorValues, testEnabled(form, key)) });
    }
    const res = await fetch('/api/trackers/' + encodeURIComponent(key) + '/test', opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* не JSON: ниже покажем HTTP-код */ }
    }
    const error = (data && data.error) ? data.error : '';
    if (!res.ok) return { ok: false, error: error || 'HTTP ' + res.status };
    return { ok: !!(data && data.ok), error: error };
  }, {
    timeoutMs: trackerTestTimeoutMs(form || btn.closest('.tracker-item')),
    // тосты только у запуска из строки: в диалоге результат виден на кнопке и в строке под телом
    toast: !form,
    origin: form ? 'dialog' : 'row',
    repaint: function () { paintTrackerTest(key); },
  });
}

function abortDialogTest(key) {
  abortTest(key, 'dialog');
}

function dialogKeydown(e) {
  if (e.key !== 'Escape' || !dialog) return;
  // открытый поповер или селект закроет глобальный обработчик, первый Escape его
  if (activeErrorPopover || openCustomSelect) return;
  closeTrackerDialog();
}

export function closeTrackerDialog(opts) {
  // оверлей прошлого диалога еще гаснет: убираем сразу, иначе дубли id до конца анимации
  if (closingOverlay) {
    cancelClose(closingOverlay);
    closingOverlay.remove();
    closingOverlay = null;
  }
  const d = dialog;
  if (!d) return true;
  const force = !!(opts && opts.force);
  if (!force && isTrackerDirty(d.root, d.mirrors.getValues())) {
    // подтверждение асинхронное: сразу отвечаем "не закрыто", закроем после ответа
    confirmDialog({ text: t('trackers.unsavedClose'), confirmLabel: t('trackers.discard'), danger: true }).then(function (ok) {
      if (ok && dialog === d) closeTrackerDialog({ force: true });
    });
    return false;
  }
  dialog = null;
  abortDialogTest(d.key);
  document.removeEventListener('keydown', dialogKeydown, true);
  closePopovers();
  closeOpenCustomSelect();
  document.querySelector('.app').inert = false;
  document.documentElement.classList.remove('modal-open');
  if (d.key) paintTestButtons(d.key);
  const back = d.add
    ? document.querySelector('#view-trackers .btn-add-tracker-open')
    : trackerCard(d.key)?.querySelector('.tracker-row');
  if (back) back.focus();
  if (force) {
    cancelClose(d.overlay);
    d.overlay.remove();
  } else {
    closingOverlay = d.overlay;
    closeAnimated(d.overlay, function () {
      d.overlay.remove();
      if (closingOverlay === d.overlay) closingOverlay = null;
    });
  }
  return true;
}

function advancedToggleHtml() {
  return html`<button type="button" class="btn btn-ghost btn-sm tr-advanced-toggle" aria-pressed="false">${t('trackers.advancedToggle')}</button>`;
}

function setAdvancedOpen(root, open) {
  root.classList.toggle('tr-advanced-open', open);
  root.querySelector('.tr-advanced-toggle').setAttribute('aria-pressed', String(open));
}

function bindAdvancedToggle(root) {
  const btn = root.querySelector('.tr-advanced-toggle');
  btn.addEventListener('click', function () { setAdvancedOpen(root, btn.getAttribute('aria-pressed') !== 'true'); });
}

function showAttention(d, ms) {
  const targets = [];
  if (ms.main.length + ms.remove.length > 0) targets.push(d.suggestions);
  if (ms.paused || ms.confirm.length > 0) targets.push(d.root.querySelector('.tr-mirrors').closest('.form-group'));
  const note = document.createElement('p');
  note.className = 'form-hint form-hint-warn tr-attn-note';
  note.textContent = rowBadgeTitle(ms);
  d.root.querySelector('.tracker-form').prepend(note);
  setAdvancedOpen(d.root, true);
  if (ms.paused || ms.confirm.length > 0) {
    // поле зеркал прячет переключатель зеркал, не расширенные: показываем его, не меняя значение
    d.root._mirrorsForced = true;
    renderMirrorSuggestions(d);
  }
  targets.forEach(function (el) { el.classList.add('tr-attn'); });
  if (targets.length) targets[0].scrollIntoView({ block: 'nearest' });
}

function mirrorSectionEl(root) {
  const group = root.querySelector('.tr-mirrors').closest('.form-group');
  const section = document.createElement('div');
  section.className = 'form-group form-group-full tr-suggestions';
  section.hidden = true;
  section.innerHTML = html`<div class="form-label">${t('trackers.suggestions')}</div><div class="tr-suggestion-list"></div>`;
  group.after(section);
  const pending = document.createElement('p');
  pending.className = 'form-hint form-hint-keep tr-mirrors-pending';
  pending.hidden = true;
  const warn = document.createElement('p');
  warn.className = 'form-hint';
  warn.textContent = t('trackers.suggestionsHint');
  group.append(pending, warn);
  return section;
}

function markMirrorChip(tr, val, chipEl) {
  if (!tr) return;
  if (tr.confirm_on === true && hostIn(tr.unconfirmed, val)) {
    chipEl.classList.add('chip-unconfirmed');
    chipEl.title = t('trackers.mirrorUnconfirmed');
    return;
  }
  // при выключенном подтверждении новые зеркала уже в ротации, но без метки их не отличить от старых
  const k = mirrorHostKey(val);
  const sug = (Array.isArray(tr.suggestions) ? tr.suggestions : []).find(function (s) {
    return s && (s.kind === 'main' || s.kind === 'remove') && mirrorHostKey(s.host) === k;
  });
  const added = (Array.isArray(tr.auto) ? tr.auto : []).some(function (a) {
    return a && a.kind === 'added' && mirrorHostKey(a.host) === k;
  });
  if (!sug && !added) return;
  chipEl.classList.add('chip-changed');
  if (!sug) chipEl.title = t('trackers.autoAdded', { host: val });
  else if (sug.kind === 'main') chipEl.title = t('trackers.suggestMain', { host: sug.host, from: sug.from || tr.domain || '' });
  else chipEl.title = t('trackers.suggestRemove', { host: sug.host });
}

function chipValueFor(d, host) {
  const k = mirrorHostKey(host);
  return d.mirrors.getValues().find(function (m) { return mirrorHostKey(m) === k; }) || '';
}

function removeMirrorChip(d, host) {
  const val = chipValueFor(d, host);
  if (!val) return;
  d.mirrors.remove(val);
  dialogTestEdit(d.key);
}

function applyMainSuggestion(d, host) {
  // замену домена сервер принимает только на адрес из присланных зеркал
  let val = chipValueFor(d, host);
  if (!val) {
    val = String(host).trim();
    d.mirrors.add(val);
  }
  const sel = d.root.querySelector('.tr-domain');
  sel.value = val;
  sel.dispatchEvent(new Event('change', { bubbles: true }));
}

async function mirrorAction(d, btn, busyKey, url, payload) {
  d.mirrorBusy.add(busyKey);
  btn.disabled = true;
  try {
    await fetchJSON(url, { method: 'POST', body: JSON.stringify(payload) });
    return true;
  } catch (err) {
    showToast(err.message, 'error');
    return false;
  } finally {
    d.mirrorBusy.delete(busyKey);
    btn.disabled = false;
  }
}

async function dismissMirrorSuggestion(d, s, btn) {
  const url = '/api/trackers/' + encodeURIComponent(d.key) + '/suggestions/dismiss';
  if (!(await mirrorAction(d, btn, s.kind + '|' + mirrorHostKey(s.host), url, { kind: s.kind, host: s.host }))) return;
  const tr = trackersByKey.get(d.key);
  if (tr && Array.isArray(tr.suggestions)) {
    tr.suggestions = tr.suggestions.filter(function (x) {
      return !(x && x.kind === s.kind && mirrorHostKey(x.host) === mirrorHostKey(s.host));
    });
  }
  if (dialog === d) renderMirrorSuggestions(d);
  updateRowBadge(d.key);
}

function renderMirrorSuggestions(d) {
  const tr = trackersByKey.get(d.key);
  if (!d.suggestions || !tr) return;
  const root = d.root;
  const chips = d.mirrors.getValues();
  const domain = root.querySelector('.tr-domain').value;
  const useMirrors = root.querySelector('.tr-use-mirrors').checked;
  syncMirrorsVisibility(root);
  hideDomainChip(root);
  const ms = mirrorState(tr, chips, domain, useMirrors ? '' : domain);
  const marked = tr.confirm_on === true ? chips.filter(function (m) { return hostIn(tr.unconfirmed, m); }) : [];
  const pending = root.querySelector('.tr-mirrors-pending');
  if (pending) {
    // поле показано принудительно при выключенном переключателе: сохранение не подтвердит зеркала
    const needSwitch = root._mirrorsForced && !useMirrors && (!!ms.paused || marked.length > 0);
    pending.textContent = needSwitch ? t('trackers.mirrorsNeedSwitch') : ms.paused ? t('trackers.suggestPaused', { host: ms.paused }) : '';
    pending.hidden = !needSwitch && !ms.paused;
  }
  const items = [];
  const rows = [];
  const shown = marked.map(mirrorHostKey);
  function addItem(host, text, reason, busyKey, acts) {
    shown.push(mirrorHostKey(host));
    const i = items.length;
    items.push(acts);
    const dis = d.mirrorBusy.has(busyKey) ? ' disabled' : '';
    const btns = acts.map(function (a, j) {
      return html`<button type="button" class="btn btn-ghost btn-icon-only" data-i="${i}" data-j="${j}" title="${a.label}" aria-label="${a.label}"${dis}>${icon(a.icon)}</button>`;
    });
    const tag = reason ? html`<span class="tr-suggestion-reason">${reason}</span>` : '';
    rows.push(html`<div class="tr-suggestion"><span class="tr-suggestion-text">${text}${tag}</span><span class="tr-suggestion-actions">${btns}</span></div>`);
  }
  ms.main.forEach(function (s) {
    addItem(s.host, t('trackers.suggestMain', { host: s.host, from: s.from || tr.domain || '' }), mirrorReason(s.reason), 'main|' + mirrorHostKey(s.host), [
      { icon: 'check', label: t('trackers.suggestApply'), run: function () { applyMainSuggestion(d, s.host); } },
      { icon: 'eyeHidden', label: t('trackers.suggestDismiss'), run: function (btn) { dismissMirrorSuggestion(d, s, btn); } },
    ]);
  });
  // при выключенных зеркалах поле с ними спрятано, убирать оттуда нечего
  if (useMirrors) ms.remove.forEach(function (s) {
    addItem(s.host, t('trackers.suggestRemove', { host: s.host }), mirrorReason(s.reason), 'remove|' + mirrorHostKey(s.host), [
      { icon: 'check', label: t('trackers.suggestApply'), run: function () { removeMirrorChip(d, s.host); } },
      { icon: 'eyeHidden', label: t('trackers.suggestDismiss'), run: function (btn) { dismissMirrorSuggestion(d, s, btn); } },
    ]);
  });
  const parts = rows.length ? [html`<div class="tr-suggestion-block">${rows}</div>`] : [];
  // журнал только для адресов без своей строки и без метки на чипе
  ms.auto.forEach(function (a) {
    if (shown.indexOf(mirrorHostKey(a.host)) !== -1) return;
    // добавленное зеркало уже выделено цветом на чипе
    if (a.kind === 'added' && hostIn(chips, a.host)) return;
    parts.push(html`<p class="tr-auto-line">${t(a.kind === 'added' ? 'trackers.autoAdded' : 'trackers.autoRemoved', { host: a.host })}</p>`);
  });
  d.mirrorItems = items;
  d.suggestions.querySelector('.tr-suggestion-list').innerHTML = html`${parts}`;
  d.suggestions.hidden = parts.length === 0;
}

function bindMirrorSuggestions(d) {
  d.suggestions.addEventListener('click', function (e) {
    const btn = e.target.closest('button[data-i]');
    if (!btn || btn.disabled) return;
    const acts = d.mirrorItems[Number(btn.dataset.i)];
    const act = acts && acts[Number(btn.dataset.j)];
    if (act) act.run(btn);
  });
  d.root.addEventListener('change', function (e) {
    if (e.target.closest('.tr-domain, .tr-use-mirrors')) renderMirrorSuggestions(d);
  });
  renderMirrorSuggestions(d);
}

async function saveTrackerDialog(d) {
  const body = gatherTrackerConfig(d.root, d.mirrors.getValues(), cardEnabled(d.key));
  const tr = trackersByKey.get(d.key);
  // подтверждаем только отмеченные чипы, которые пользователь мог увидеть: поле зеркал спрятано при выключенном переключателе
  const confirm = d.root.querySelector('.tr-use-mirrors').checked && tr && tr.confirm_on === true
    ? d.mirrors.getValues().filter(function (m) { return hostIn(tr.unconfirmed, m); })
    : [];
  setSaving(d.saveBtn, true);
  let res;
  try {
    res = await fetchJSON('/api/trackers/' + encodeURIComponent(d.key), {
      method: 'PUT',
      // зеркала, которые видел диалог: дописанные задачей после открытия сервер не потеряет
      body: JSON.stringify(Object.assign({}, body, { mirrors_seen: d.mirrorsSeen, mirrors_confirm: confirm })),
    });
  } catch (err) {
    showToast(err.message, 'error');
    setSaving(d.saveBtn, false);
    return;
  }
  showToast(t('trackers.saved', { key: d.key }), 'success');
  // 204 дает null, 200 несет зеркала, которые задача успела убрать после открытия диалога
  const dropped = res && Array.isArray(res.removed_mirrors) ? res.removed_mirrors : [];
  if (dropped.length) showToast(t('trackers.mirrorsRemovedOnSave', { list: dropped.join(', ') }), 'info');
  // сохранение подтверждает оставленные отмеченные зеркала, метки и пауза приходят от сервера
  const prev = trackersByKey.get(d.key);
  const confirmed = !!prev && body.mirrors.some(function (m) { return hostIn(prev.unconfirmed, m); });
  // домен сверяем с сохраненным, не со снимком: у старого трекера снимок уже держит зеркало основным
  if ((prev && body.domain !== prev.domain) || dropped.length || confirmed) {
    // сервер сам переставил домен и зеркала, берем итог у него
    if (dialog === d) closeTrackerDialog({ force: true });
    loadTrackers();
    return;
  }
  const merged = Object.assign({}, trackersByKey.get(d.key), body);
  trackersByKey.set(d.key, merged);
  const item = trackerCard(d.key);
  if (item) {
    item._trackerSaved = body;
    updateCardHeader(item, merged);
    updateRowBadge(d.key);
    applyTrackerFilter();
    paintTestButtons(d.key);
  }
  if (dialog === d) closeTrackerDialog({ force: true });
}

async function removeTrackerFromDialog(d) {
  const tracker = trackersByKey.get(d.key);
  const ok = await confirmDialog({
    text: t('trackers.removeConfirm', { name: (tracker && tracker.name) || d.key }),
    confirmLabel: t('trackers.remove'),
    danger: true,
  });
  if (!ok || dialog !== d) return;
  d.removeBtn.disabled = true;
  try {
    await fetchJSON('/api/trackers/' + encodeURIComponent(d.key), { method: 'DELETE' });
  } catch (err) {
    showToast(err.message, 'error');
    d.removeBtn.disabled = false;
    return;
  }
  forgetTest(d.key);
  showToast(t('trackers.removed', { key: d.key }), 'success');
  if (dialog === d) closeTrackerDialog({ force: true });
  loadTrackers();
}

async function addTrackerFromDialog(d) {
  if (!d.key) return;
  const key = d.key;
  const body = gatherTrackerConfig(d.root, d.mirrors.getValues(), true);
  setSaving(d.saveBtn, true);
  try {
    await fetchJSON('/api/trackers/' + encodeURIComponent(key), {
      method: 'POST',
      body: JSON.stringify(Object.assign({}, body, { mirrors_seen: d.mirrorsSeen })),
    });
  } catch (err) {
    showToast(err.message, 'error');
    setSaving(d.saveBtn, false);
    return;
  }
  showToast(t('trackers.added', { key: key }), 'success');
  if (dialog === d) closeTrackerDialog({ force: true });
  loadTrackers();
}

function selectAddType(d, tp) {
  const root = d.root;
  abortDialogTest(d.key);
  d.key = tp.key;
  const holder = root.querySelector('.tracker-add-form');
  holder.innerHTML = trackerFormHtml(tp, tp.key, tp.publicity || 'public', tp.mirrors || []);
  // трекера еще нет, раскрывать маску по /secret нечем: сервер ответит 404
  holder.querySelectorAll('[data-reveal]').forEach(function (el) { el.removeAttribute('data-reveal'); });
  const hosts0 = trackerHosts(tp.domain, tp.mirrors);
  root._trackerBase = tp.domain;
  d.mirrorsSeen = hosts0.slice();
  d.mirrors = initChipInput(
    holder.querySelector('.tr-mirrors'),
    null,
    hosts0,
    null,
    function (values) {
      syncTrackerDomainSelects(root, tp.domain || '', values);
    },
    { pinned: function (val) { return mirrorHostKey(val) === mirrorHostKey(root.querySelector('.tr-domain').value); } }
  );
  hideDomainChip(root);
  holder.querySelector('.tr-domain').addEventListener('change', function () { hideDomainChip(root); });
  initCustomSelects(holder);
  initPasswordToggles(root);
  initHintToggles(holder);
  syncMirrorsVisibility(holder);
  holder.querySelector('.tr-use-mirrors').addEventListener('change', function () { syncMirrorsVisibility(holder); });
  hydrateIcons(holder);
  root._trackerSaved = Object.assign(trackerConfigFromApi(tp), { enabled: true, mirrors: mirrorsForSave(hosts0, tp.domain, tp.domain) });

  if (d.testBtn) d.testBtn.remove();
  forgetTest(tp.key);
  paintTrackerTest(tp.key);
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn btn-ghost btn-icon-only btn-test btn-test-tracker';
  btn.setAttribute('data-key', tp.key);
  d.saveBtn.before(btn);
  d.testBtn = btn;
  bindTrackerTestButton(btn, function () { testTracker(tp.key, btn, root, d.mirrors.getValues()); });
  d.saveBtn.disabled = false;
}

function openAddTrackerDialog() {
  const available = trackerTypes.filter(function (tp) { return !tp.added && !trackersByKey.has(tp.key); });
  if (available.length === 0) return;
  closeTrackerDialog({ force: true });
  const titleId = 'tracker-dialog-title';
  const options = available.map(function (tp) {
    return html`<option value="${tp.key}">${tp.name || tp.key} (${tp.domain})</option>`;
  });
  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  overlay.innerHTML = html`<div class="modal" role="dialog" aria-modal="true" aria-labelledby="${titleId}" tabindex="-1"><div class="modal-head"><h2 class="modal-title" id="${titleId}">${t('trackers.add')}</h2>${advancedToggleHtml()}<button type="button" class="btn btn-ghost btn-icon-only modal-close" title="${t('common.close')}" aria-label="${t('common.close')}"><span data-icon="x"></span></button></div><div class="modal-body"><div class="form-group"><label class="form-label" for="tr-add-type">${t('trackers.addType')}</label><select id="tr-add-type" class="select tr-add-type"><option value="" disabled selected>${t('trackers.addTypePlaceholder')}</option>${options}</select></div><div class="tracker-add-form"></div></div><p class="test-result modal-test-result" role="status" hidden></p><div class="modal-foot"><button type="button" class="btn btn-primary btn-save-tracker" disabled>${t('trackers.addConfirm')}</button></div></div>`;
  const root = overlay.querySelector('.modal');
  root._addMode = true;
  const saveBtn = root.querySelector('.btn-save-tracker');
  const d = { key: null, add: true, overlay: overlay, root: root, mirrors: { getValues: function () { return []; } }, saveBtn: saveBtn, testBtn: null, line: root.querySelector('.modal-test-result') };
  dialog = d;

  const typeSelect = root.querySelector('.tr-add-type');
  typeSelect.addEventListener('change', function () {
    const tp = available.find(function (x) { return x.key === typeSelect.value; });
    if (tp) selectAddType(d, tp);
  });
  saveBtn.addEventListener('click', function () { addTrackerFromDialog(d); });
  bindAdvancedToggle(root);
  root.querySelector('.modal-close').addEventListener('click', function () { closeTrackerDialog(); });
  root.addEventListener('input', function () { if (d.key) dialogTestEdit(d.key); });
  root.addEventListener('change', function () { if (d.key) dialogTestEdit(d.key); });

  let downOnBackdrop = false;
  overlay.addEventListener('pointerdown', function (e) { downOnBackdrop = e.target === overlay; });
  overlay.addEventListener('click', function (e) {
    if (e.target === overlay && downOnBackdrop) closeTrackerDialog();
    downOnBackdrop = false;
  });
  document.addEventListener('keydown', dialogKeydown, true);

  document.body.appendChild(overlay);
  document.querySelector('.app').inert = true;
  document.documentElement.classList.add('modal-open');
  initCustomSelects(root);
  hydrateIcons(overlay);
  root.focus();
}

function openTrackerDialog(key) {
  const tracker = trackersByKey.get(key);
  const item = trackerCard(key);
  if (!tracker || !item) return;
  closeTrackerDialog({ force: true });
  const titleId = 'tracker-dialog-title';
  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  overlay.innerHTML = html`<div class="modal" role="dialog" aria-modal="true" aria-labelledby="${titleId}" tabindex="-1"><div class="modal-head"><h2 class="modal-title" id="${titleId}">${tracker.name || key}</h2>${advancedToggleHtml()}<button type="button" class="btn btn-ghost btn-icon-only modal-close" title="${t('common.close')}" aria-label="${t('common.close')}"><span data-icon="x"></span></button></div><div class="modal-body">${trackerFormHtml(tracker, key, tracker.publicity || 'public', tracker.mirrors || [])}</div><p class="test-result modal-test-result" role="status" hidden></p><div class="modal-foot"><button type="button" class="btn btn-ghost btn-remove-tracker"><span data-icon="trash"></span><span>${t('trackers.remove')}</span></button><button type="button" class="btn btn-ghost btn-icon-only btn-test btn-test-tracker" data-key="${key}"></button><button type="button" class="btn btn-primary btn-save-tracker">${t('common.save')}</button></div></div>`;
  const root = overlay.querySelector('.modal');
  const saved = item._trackerSaved;
  // форма стартует с выбором trackerDomainChoice, снимок обязан совпасть с ней, иначе диалог сразу "грязный"
  const choice = trackerDomainChoice(tracker, tracker.mirrors || []);
  const hosts0 = trackerHosts(tracker.domain, tracker.mirrors);
  root._trackerBase = tracker.domain;
  root._trackerSaved = Object.assign({}, saved, {
    domain: choice.domain,
    active_domain: choice.useMirrors ? '' : choice.domain,
    mirrors: mirrorsForSave(hosts0, choice.domain, tracker.domain),
  });
  const section = mirrorSectionEl(root);

  const mirrors = initChipInput(
    root.querySelector('.tr-mirrors'),
    null,
    hosts0,
    null,
    function (values) {
      syncTrackerDomainSelects(root, tracker.domain || '', values);
      if (dialog && dialog.root === root) renderMirrorSuggestions(dialog);
    },
    {
      decorate: function (val, chipEl) { markMirrorChip(trackersByKey.get(key), val, chipEl); },
      pinned: function (val) { return mirrorHostKey(val) === mirrorHostKey(root.querySelector('.tr-domain').value); },
    }
  );
  hideDomainChip(root);

  const testBtn = root.querySelector('.btn-test-tracker');
  const saveBtn = root.querySelector('.btn-save-tracker');
  const removeBtn = root.querySelector('.btn-remove-tracker');
  const d = { key: key, overlay: overlay, root: root, mirrors: mirrors, saveBtn: saveBtn, removeBtn: removeBtn, line: root.querySelector('.modal-test-result'), suggestions: section, mirrorsSeen: hosts0.slice(), mirrorItems: [], mirrorBusy: new Set() };
  dialog = d;
  bindAdvancedToggle(root);
  const attn = trackerMirrorState(tracker);
  bindMirrorSuggestions(d);

  bindTrackerTestButton(testBtn, function () { testTracker(key, testBtn, root, mirrors.getValues()); });
  paintTestLine(d.line, key);
  saveBtn.addEventListener('click', function () { saveTrackerDialog(d); });
  removeBtn.addEventListener('click', function () { removeTrackerFromDialog(d); });
  root.querySelector('.modal-close').addEventListener('click', function () { closeTrackerDialog(); });
  root.addEventListener('input', function () { dialogTestEdit(key); });
  root.addEventListener('change', function () { dialogTestEdit(key); });

  // выделение в поле, дотянутое мышью за рамку, дает click по подложке. Закрываем,
  // только если и нажатие, и отпускание были на ней
  let downOnBackdrop = false;
  overlay.addEventListener('pointerdown', function (e) { downOnBackdrop = e.target === overlay; });
  overlay.addEventListener('click', function (e) {
    if (e.target === overlay && downOnBackdrop) closeTrackerDialog();
    downOnBackdrop = false;
  });
  document.addEventListener('keydown', dialogKeydown, true);

  document.body.appendChild(overlay);
  document.querySelector('.app').inert = true;
  document.documentElement.classList.add('modal-open');
  initCustomSelects(root);
  initPasswordToggles(root);
  // раскрытие секрета не правка: обновляем снимок, иначе при закрытии
  // диалог спросит про несохраненные изменения
  root.addEventListener('secretreveal', function (e) {
    const field = e.target.dataset.field;
    if (field in root._trackerSaved) root._trackerSaved[field] = e.detail.value;
  });
  initHintToggles(root);
  hydrateIcons(overlay);
  root.focus();
  if (attn.pending) showAttention(d, attn);
}

function trackerKeys(list) {
  return Array.from(list.querySelectorAll(':scope > .tracker-item'), function (item) { return item.getAttribute('data-key'); });
}

function saveTrackerOrder(keys) {
  return fetchJSON('/api/trackers/order', { method: 'PUT', body: JSON.stringify({ order: keys }) });
}

function initTrackerDrag(list) {
  // слушатели на самом списке: renderTrackers пересобирает его целиком, и они уходят
  // вместе с ним. На document только once-слушатель
  let dragging = null;
  let before = [];
  let saving = false;

  function canDrag() {
    return !saving && !list.classList.contains('is-filtered') && !document.documentElement.classList.contains('mobile-layout');
  }

  function clearDraggable() {
    list.querySelectorAll('.tracker-item[draggable]').forEach(function (item) { item.removeAttribute('draggable'); });
  }

  function restoreOrder(keys) {
    const byKey = new Map();
    list.querySelectorAll(':scope > .tracker-item').forEach(function (item) { byKey.set(item.getAttribute('data-key'), item); });
    keys.forEach(function (k) { if (byKey.has(k)) list.appendChild(byKey.get(k)); });
  }

  list.addEventListener('mousedown', function (e) {
    if (e.button !== 0 || !e.target.closest('.tracker-drag-handle') || !canDrag()) return;
    e.target.closest('.tracker-item').draggable = true;
    // без dragstart отпускание кнопки возвращает карточке обычное поведение
    document.addEventListener('mouseup', clearDraggable, { once: true });
  });

  list.addEventListener('dragstart', function (e) {
    const item = e.target.closest ? e.target.closest('.tracker-item') : null;
    if (!item || !item.draggable || !canDrag()) {
      e.preventDefault();
      return;
    }
    dragging = item;
    before = trackerKeys(list);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', item.getAttribute('data-key'));
    item.classList.add('is-dragging');
  });

  list.addEventListener('dragover', function (e) {
    if (!dragging) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const over = e.target.closest ? e.target.closest('.tracker-item') : null;
    if (!over || over === dragging) return;
    const rect = over.getBoundingClientRect();
    const ref = e.clientY > rect.top + rect.height / 2 ? over.nextElementSibling : over;
    if (ref !== dragging) list.insertBefore(dragging, ref);
  });

  list.addEventListener('drop', function (e) {
    if (dragging) e.preventDefault();
  });

  list.addEventListener('dragend', async function () {
    const item = dragging;
    if (!item) return;
    dragging = null;
    item.classList.remove('is-dragging');
    clearDraggable();
    const keys = trackerKeys(list);
    if (keys.join('\n') === before.join('\n')) return;
    saving = true;
    try {
      await saveTrackerOrder(keys);
    } catch (err) {
      restoreOrder(before);
      showToast(err.message, 'error');
    }
    saving = false;
  });
}

export async function loadTrackers() {
  const el = document.getElementById('view-trackers');
  el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
  try {
    const fetches = [fetchJSON('/api/trackers'), fetchJSON('/api/trackers/types')];
    if (!integrationsCache) fetches.push(fetchJSON('/api/integrations'));
    const results = await Promise.all(fetches);
    if (!integrationsCache && results[2]) setIntegrationsCache(results[2]);
    renderTrackers(el, results[0] || [], results[1] || []);
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export function renderTrackers(el, trackers, types) {
  closeTrackerDialog({ force: true });
  trackerTypes = types || [];
  trackersByKey.clear();
  trackers.forEach(function (tr) { trackersByKey.set(tr.key, tr); });

  let list;
  let filter = '';
  if (trackers.length === 0) {
    list = html`<p class="text-muted tracker-empty">${t('trackers.empty')}</p>`;
  } else {
    const items = trackers.map(function (tr) { return trackerItemHtml(tr); });
    list = html`<div class="tracker-list"><div class="tracker-list-head" aria-hidden="true"><span>${t('trackers.colName')}</span><span>${t('trackers.colDomain')}</span><span>${t('trackers.proxy')}</span><span>${t('trackers.hourLimit')}</span></div>${items}</div><p class="text-muted tracker-filter-empty" hidden>${t('trackers.filterEmpty')}</p>`;
    filter = html`<input type="search" class="input tracker-filter" autocomplete="off" placeholder="${t('trackers.filterPlaceholder')}" aria-label="${t('trackers.filterPlaceholder')}">`;
  }

  const canAdd = trackerTypes.some(function (tp) { return !tp.added; });
  const addTitle = canAdd ? t('trackers.add') : t('trackers.addAllAdded');
  const addBtn = html`<button type="button" class="btn btn-primary btn-add-tracker-open"${canAdd ? '' : ' disabled'} title="${addTitle}">${t('trackers.add')}</button>`;

  el.innerHTML = html`<div class="page-header page-header-row tracker-page-header"><h1 class="page-title">${t('trackers.title')}</h1><div class="page-header-actions">${filter}${addBtn}</div></div>${list}`;
  el.querySelector('.btn-add-tracker-open').addEventListener('click', openAddTrackerDialog);

  const listEl = el.querySelector('.tracker-list');
  if (listEl) initTrackerDrag(listEl);

  const filterInput = el.querySelector('.tracker-filter');
  if (filterInput) filterInput.addEventListener('input', applyTrackerFilter);

  el.querySelectorAll('.tracker-item').forEach(function (item) {
    const key = item.getAttribute('data-key');
    item._trackerSaved = trackerConfigFromApi(trackersByKey.get(key));

    const row = item.querySelector('.tracker-row');
    row.addEventListener('click', function (e) {
      if (e.target.closest('.tracker-row-toggle') || e.target.closest('.btn-test-tracker') || e.target.closest('.tracker-drag-handle')) return;
      openTrackerDialog(key);
    });
    // keydown прилетает и от вложенных переключателя и кнопки теста. Без проверки
    // Enter и пробел на них открывали бы диалог вместо своего дейтсвия
    row.addEventListener('keydown', function (e) {
      if (e.target !== row) return;
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openTrackerDialog(key);
      }
    });

    const checkbox = item.querySelector('.tr-enabled');
    checkbox.addEventListener('change', async function (e) {
      e.stopPropagation();
      const enabled = checkbox.checked;
      const prev = !enabled;
      setTrackerItemEnabled(item, enabled);
      checkbox.disabled = true;
      try {
        await fetchJSON('/api/trackers/' + encodeURIComponent(key), {
          method: 'PUT',
          body: JSON.stringify({ enabled: enabled }),
        });
        showToast(t('trackers.enabledSaved', {
          key: key,
          state: enabled ? t('trackers.enabledOn') : t('trackers.enabledOff'),
        }), 'success');
        if (item._trackerSaved) item._trackerSaved.enabled = enabled;
      } catch (err) {
        checkbox.checked = prev;
        setTrackerItemEnabled(item, prev);
        showToast(err.message, 'error');
      }
      checkbox.disabled = false;
    });

    const testBtn = item.querySelector('.btn-test-tracker');
    bindTrackerTestButton(testBtn, function () { testTracker(key, testBtn, null, []); });
  });

  el.querySelectorAll('.tracker-row-toggle').forEach(function (label) {
    label.addEventListener('click', function (e) { e.stopPropagation(); });
  });

  hydrateIcons(el);
}
