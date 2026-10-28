import { fetchJSON, showToast } from './api.js';
import { browserDeviceCount, browserPanelHtml, initBrowserPush, reloadBrowserDevices } from './browserpush.js';
import { cancelClose, closeAnimated, initHintToggles, initPasswordToggles, setSaving, updateSegmentThumb } from './components.js';
import { hydrateIcons } from './icons.js';
import { initCustomSelects } from './select.js';
import { abortTest, bindTestButton, forgetTest, paintTestButton, paintTestLine, resetTest, runTest } from './testbutton.js';
import { SECRET_MASK, html, secureInputAttrs } from './util.js';

export let integrationsCache = null;
// переключатель живет в расписании (PUT /api/schedule), карточка источника зеркал только показывает его.
// null: старый сервер не знает mirror_confirm, переключатель не рисуем
let mirrorConfirm = null;

let clientDrafts = { qbit: {}, transmission: {}, deluge: {}, downloadstation: {} };
let clientDraftType = 'qbit';

const NOTIFY_CHANNELS = ['telegram', 'webhook', 'email', 'pushover', 'browser'];
const NOTIFY_CHANNEL_KEYS = {
  telegram: 'integrations.notifyChannelTelegram',
  webhook: 'integrations.notifyChannelWebhook',
  email: 'integrations.notifyChannelEmail',
  pushover: 'integrations.notifyChannelPushover',
  browser: 'integrations.notifyChannelBrowser',
};
const NOTIFY_EVENTS = ['updated', 'broken', 'tracker_down', 'tracker_up', 'mirror_switched', 'check_failed', 'no_space'];
const NOTIFY_EVENT_KEYS = {
  updated: 'integrations.notifyEventUpdated',
  broken: 'integrations.notifyEventBroken',
  tracker_down: 'integrations.notifyEventTrackerDown',
  tracker_up: 'integrations.notifyEventTrackerUp',
  mirror_switched: 'integrations.notifyEventMirrorSwitched',
  check_failed: 'integrations.notifyEventCheckFailed',
  no_space: 'integrations.notifyEventNoSpace',
};
const NOTIFY_DEFAULT_EVENTS = ['updated', 'broken', 'tracker_down', 'tracker_up', 'mirror_switched', 'no_space'];
const EMAIL_DEFAULT_PORTS = { starttls: 587, tls: 465, none: 25 };
const INTEGRATION_TEST_KINDS = ['client', 'flaresolverr', 'proxy', 'mirror_source', 'notify'];

export function setIntegrationsCache(doc) {
  // trackers.js тоже кладет сюда кеш, а чужую привязку модуль менять не может
  integrationsCache = doc;
}

export function parseDownloadProxy(url) {
  if (!url) return { type: 'http', address: '' };
  const m = String(url).match(/^(https?|socks5):\/\//i);
  if (m) {
    const scheme = m[1].toLowerCase();
    const type = scheme === 'socks5' ? 'socks5' : (scheme === 'https' ? 'https' : 'http');
    return { type: type, address: url.replace(/^[^:]+:\/\//i, '') };
  }
  return { type: 'http', address: String(url) };
}

export function buildDownloadProxy(type, address) {
  const addr = String(address || '').trim();
  if (!addr) return '';
  const scheme = type === 'socks5' ? 'socks5' : (type === 'https' ? 'https' : 'http');
  if (/^(https?|socks5):\/\//i.test(addr)) {
    return addr.replace(/^[^:]+:\/\//i, scheme + '://');
  }
  return scheme + '://' + addr;
}

function flareTimeoutMs() {
  // в поле секунды, сервер хранит миллисекунды. Дробные секунды не теряем
  const sec = parseFloat(document.getElementById('flare-timeout').value);
  return sec > 0 ? Math.round(sec * 1000) : 0;
}

export function getProxyDownloadValue() {
  const typeEl = document.getElementById('proxy-type');
  const addrEl = document.getElementById('proxy-download');
  if (!typeEl || !addrEl) return '';
  return buildDownloadProxy(typeEl.value, addrEl.value);
}


export async function loadIntegrations() {
  const el = document.getElementById('view-integrations');
  // пока форма заменена заглушкой, сверять с ней нечего
  intSnap = null;
  el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
  try {
    const [data, sch] = await Promise.all([
      fetchJSON('/api/integrations'),
      fetchJSON('/api/schedule').catch(function () { return null; }),
    ]);
    integrationsCache = data;
    mirrorConfirm = sch && typeof sch.mirror_confirm === 'boolean' ? sch.mirror_confirm : null;
    renderIntegrations(el, integrationsCache);
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export function renderIntegrations(el, data) {
  const clientVal = data.client || 'qbit';
  // черновики всех типов клиента держим в памяти: поля в карточке общие, и смена типа
  // стирала бы уже введенные логин и пароль
  clientDrafts = {
    qbit: Object.assign({ host: '', username: '', password: '' }, data.qbit),
    transmission: Object.assign({ host: '', username: '', password: '' }, data.transmission),
    deluge: Object.assign({ host: '', password: '' }, data.deluge),
    downloadstation: Object.assign({ host: '', username: '', password: '' }, data.downloadstation),
  };
  clientDraftType = clientVal;
  const cur = clientDrafts[clientVal] || clientDrafts.qbit;
  const f = data.flaresolverr || {};
  const p = data.proxy || {};
  const proxyParts = parseDownloadProxy(p.download_proxy || '');
  const ms = data.mirror_source || {};
  const n = data.notifications || {};
  const filledType = initialNotifyChannel(n);
  const notifyType = filledType || 'telegram';
  notifyTabAuto = !filledType;
  // форма заново из данных сервера, прошлый результат к ней уже не относится
  INTEGRATION_TEST_KINDS.forEach(function (kind) { forgetTest('int:' + kind); });

  el.innerHTML = html`<div class="page-header"><div class="page-header-row int-page-header"><h1 class="page-title">${t('integrations.title')}</h1><div class="page-header-actions"><button type="button" class="btn btn-primary" id="int-save" disabled>${t('common.save')}</button></div></div></div><div class="stack" id="int-stack">${clientCardHtml(clientVal, cur)}${flareCardHtml(f)}${proxyCardHtml(proxyParts)}${mirrorSourceCardHtml(ms)}${notifyCardHtml(n, notifyType)}${metricsCardHtml(data.metrics || {})}</div>`;

  const stack = el.querySelector('#int-stack');
  intSaving = false;
  el.querySelector('#int-save').addEventListener('click', saveIntegrations);
  stack.addEventListener('input', updateIntSave);
  stack.addEventListener('change', updateIntSave);
  stack.addEventListener('secretreveal', onIntSecretReveal);

  const metricsGenerate = document.getElementById('metrics-generate');
  if (metricsGenerate) {
    metricsGenerate.addEventListener('click', function () {
      const bytes = crypto.getRandomValues(new Uint8Array(32));
      const input = document.getElementById('metrics-token');
      input.value = Array.from(bytes, function (b) { return b.toString(16).padStart(2, '0'); }).join('');
      // программная запись событий не шлет, а Сохранить смотрит на них
      input.dispatchEvent(new Event('input', { bubbles: true }));
      // новый токен нужно скопировать в Prometheus, поэтому сразу показываем его
      const wrap = input.closest('.input-password-wrap');
      const revealed = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      if (!revealed) wrap.querySelector('.input-password-toggle').click();
      input.focus();
      input.select();
    });
  }

  el.querySelectorAll('.btn-test-int').forEach(function (btn) {
    const kind = btn.getAttribute('data-kind');
    const key = btn.getAttribute('data-key');
    const repaint = function () { paintIntegrationTest(kind); };
    bindTestButton(btn, key, function () {
      // карточка клиента общая на все типы, а бэкенду нужен конкретный
      const apiKind = kind === 'client' ? getSelectedClient() : kind;
      runTest(key, async function (signal) {
        const res = await fetchJSON('/api/integrations/test', {
          method: 'POST',
          body: JSON.stringify({ kind: apiKind, config: getIntegrationTestConfig(apiKind) }),
          signal: signal,
        });
        if (apiKind === 'notify' && getNotifyChannelType() === 'browser') reloadBrowserDevices();
        return { ok: !!res.ok, error: res.error || '' };
      }, { repaint: repaint });
    }, repaint);
    const card = btn.closest('.card');
    const onEdit = function () {
      resetTest(key);
      repaint();
    };
    card.addEventListener('input', onEdit);
    card.addEventListener('change', onEdit);
    repaint();
  });

  initCustomSelects(el);
  initPasswordToggles(el);
  initHintToggles(el);
  hydrateIcons(el);

  applyClientHint(el, clientVal);
  const clientSelect = document.getElementById('download-client');
  if (clientSelect) {
    clientSelect.addEventListener('change', function () {
      clientDrafts[clientDraftType] = readClientFields();
      abortTest('int:client');
      clientDraftType = clientSelect.value || 'qbit';
      writeClientFields(clientDrafts[clientDraftType] || {});
      document.getElementById('client-password').dataset.revealField = clientDraftType + '.password';
      applyClientHint(el, clientDraftType);
    });
  }

  const mirrorTypeSelect = document.getElementById('mirror-source-type');
  if (mirrorTypeSelect) {
    mirrorTypeSelect.addEventListener('change', function () {
      applyMirrorSourceType(el, mirrorTypeSelect.value);
    });
  }

  applyNotifyChannelType(el, notifyType);
  const notifyTabs = document.getElementById('notify-tabs');
  if (notifyTabs) {
    notifyTabs.addEventListener('click', function (e) {
      const tab = e.target.closest('.notify-tab');
      if (tab) selectNotifyChannel(el, tab.getAttribute('data-channel'), false);
    });
    notifyTabs.addEventListener('keydown', function (e) {
      const tabs = Array.from(notifyTabs.querySelectorAll('.notify-tab'));
      const idx = tabs.indexOf(e.target.closest('.notify-tab'));
      if (idx < 0) return;
      let next = -1;
      if (e.key === 'ArrowRight') next = (idx + 1) % tabs.length;
      else if (e.key === 'ArrowLeft') next = (idx - 1 + tabs.length) % tabs.length;
      else if (e.key === 'Home') next = 0;
      else if (e.key === 'End') next = tabs.length - 1;
      if (next < 0) return;
      e.preventDefault();
      selectNotifyChannel(el, tabs[next].getAttribute('data-channel'), true);
    });
  }
  const notifyBody = document.getElementById('notify-card-body');
  if (notifyBody) {
    const refresh = function () { updateNotifyTabs(el); updateNotifyEventCounts(el); };
    notifyBody.addEventListener('input', refresh);
    notifyBody.addEventListener('change', refresh);
    notifyBody.addEventListener('click', onNotifyEventsClick);
    const security = document.getElementById('notify-email-security');
    if (security) security.addEventListener('change', applyEmailPortPlaceholder);
    applyEmailPortPlaceholder();
    refresh();
    initBrowserPush(el, function () {
      if (notifyTabAuto && browserDeviceCount() > 0) selectNotifyChannel(el, 'browser', false);
      notifyTabAuto = false;
      updateNotifyTabs(el);
      paintIntegrationTest('notify');
    });
  }

  intSnap = snapshotIntegrations();
}

const INT_SECTIONS = {
  client: {
    read: readClientSection,
    apply: function (doc, v) {
      doc.client = v.client;
      doc.qbit = Object.assign(doc.qbit || {}, v.qbit);
      doc.transmission = Object.assign(doc.transmission || {}, v.transmission);
      doc.deluge = Object.assign(doc.deluge || {}, v.deluge);
      doc.downloadstation = Object.assign(doc.downloadstation || {}, v.downloadstation);
    },
  },
  flaresolverr: {
    read: function () {
      return {
        url: document.getElementById('flare-url').value,
        timeout_ms: flareTimeoutMs(),
      };
    },
    apply: function (doc, v) { doc.flaresolverr = Object.assign(doc.flaresolverr || {}, v); },
  },
  proxy: {
    read: function () { return { download_proxy: getProxyDownloadValue() }; },
    apply: function (doc, v) { doc.proxy = Object.assign(doc.proxy || {}, v); },
  },
  mirror_source: {
    read: getMirrorSourceFormValue,
    apply: function (doc, v) { doc.mirror_source = v; },
  },
  notifications: {
    read: getNotifyFormValue,
    apply: function (doc, v) { doc.notifications = v; },
  },
  metrics: {
    read: getMetricsFormValue,
    apply: function (doc, v) { doc.metrics = v; },
  },
  mirror_confirm: {
    read: function () {
      const box = document.getElementById('mirror_confirm');
      return box ? box.checked : false;
    },
    apply: function () {},
  },
};

let intSnap = null;
let intSaving = false;
// устройства браузера приходят позже формы: пока вкладку никто не выбирал, переключаем на них
let notifyTabAuto = false;

function readClientSection() {
  const drafts = Object.assign({}, clientDrafts);
  drafts[clientDraftType] = readClientFields();
  const pick = function (d, withUser) {
    const v = { host: d.host || '', password: d.password || '' };
    if (withUser) v.username = d.username || '';
    return v;
  };
  return {
    client: getSelectedClient(),
    qbit: pick(drafts.qbit, true),
    transmission: pick(drafts.transmission, true),
    deluge: pick(drafts.deluge, false),
    downloadstation: pick(drafts.downloadstation, true),
  };
}

function readIntSections() {
  const all = {};
  Object.keys(INT_SECTIONS).forEach(function (name) { all[name] = INT_SECTIONS[name].read(); });
  return all;
}

function snapshotIntegrations() {
  const all = readIntSections();
  const snap = {};
  Object.keys(all).forEach(function (name) { snap[name] = JSON.stringify(all[name]); });
  return snap;
}

function intDirtySections() {
  if (!intSnap) return [];
  return Object.keys(INT_SECTIONS).filter(function (name) {
    return JSON.stringify(INT_SECTIONS[name].read()) !== intSnap[name];
  });
}

export function hasUnsavedIntegrations() {
  return intDirtySections().length > 0;
}

function updateIntSave() {
  const btn = document.getElementById('int-save');
  if (!btn) return;
  setSaving(btn, intSaving);
  btn.disabled = intSaving || !hasUnsavedIntegrations();
}

function carryReveal(snap, masked, cur) {
  Object.keys(cur).forEach(function (k) {
    if (cur[k] && typeof cur[k] === 'object') carryReveal(snap[k], masked[k], cur[k]);
    else if (masked[k] !== cur[k]) snap[k] = cur[k];
  });
}

function onIntSecretReveal(e) {
  // раскрытие секрета не правка: подменяем в снимке маску на настоящее значение,
  // иначе Сохранить загорелся бы от одного клика по глазу
  if (!intSnap) return;
  const input = e.target;
  const shown = input.value;
  input.value = SECRET_MASK;
  const masked = readIntSections();
  input.value = shown;
  const cur = readIntSections();
  Object.keys(INT_SECTIONS).forEach(function (name) {
    const snap = JSON.parse(intSnap[name]);
    carryReveal(snap, masked[name], cur[name]);
    intSnap[name] = JSON.stringify(snap);
  });
  updateIntSave();
}

async function saveIntegrations() {
  const dirty = intDirtySections();
  if (!dirty.length || intSaving) return;
  const snap = intSnap;
  // снимок того, что ушло: правки за время запроса остаются несохраненными
  const sent = {};
  dirty.forEach(function (name) { sent[name] = JSON.stringify(INT_SECTIONS[name].read()); });
  const doc = buildIntegrationsDoc(dirty);
  intSaving = true;
  updateIntSave();
  try {
    if (dirty.some(function (name) { return name !== 'mirror_confirm'; })) {
      await fetchJSON('/api/integrations', { method: 'PUT', body: JSON.stringify(doc) });
      integrationsCache = doc;
    }
    if (dirty.includes('mirror_confirm')) {
      const v = JSON.parse(sent.mirror_confirm);
      await fetchJSON('/api/schedule', { method: 'PUT', body: JSON.stringify({ mirror_confirm: v }) });
      mirrorConfirm = v;
    }
    dirty.forEach(function (name) { snap[name] = sent[name]; });
    showToast(t('integrations.saved'), 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
  intSaving = false;
  updateIntSave();
}

function testButtonHtml(kind) {
  return html`<button type="button" class="btn btn-ghost btn-icon-only btn-test btn-test-int" data-kind="${kind}" data-key="int:${kind}"><span data-icon="activity"></span></button>`;
}

function testLineHtml(kind) {
  return html`<p class="test-result" data-kind="${kind}" role="status" hidden></p>`;
}

function clientCardHtml(clientVal, cur) {
  const opt = function (value, key) {
    return html`<option value="${value}"${clientVal === value ? ' selected' : ''}>${t(key)}</option>`;
  };
  return html`<div class="card"><div class="card-header"><h2 class="card-title">${t('integrations.client')}</h2>${testButtonHtml('client')}</div><div class="card-body" autocomplete="off">${testLineHtml('client')}<div class="form-row"><div class="form-group"><label class="form-label" for="download-client">${t('integrations.clientType')}</label><select class="select" id="download-client">${opt('qbit', 'integrations.qbit')}${opt('transmission', 'integrations.transmission')}${opt('deluge', 'integrations.deluge')}${opt('downloadstation', 'integrations.downloadstation')}</select><p class="form-hint">${t('integrations.clientHint')}</p></div><div class="form-group"><label class="form-label" for="client-host">${t('integrations.host')}</label><input type="text" class="input" id="client-host" autocomplete="off" name="dc-host-field" value="${cur.host}"></div></div><div class="form-row"><div class="form-group"><label class="form-label" for="client-username">${t('integrations.username')}</label><input type="text" class="input" id="client-username" autocomplete="off" name="dc-user-field" value="${cur.username}"></div><div class="form-group"><label class="form-label" for="client-password">${t('integrations.password')}</label><div class="input-password-wrap"><input id="client-password" data-reveal="/api/integrations/secret" data-reveal-field="${clientVal}.password"${secureInputAttrs('dc-secret')} value="${cur.password}"><button type="button" class="input-password-toggle" aria-label="${t('trackers.showCredentials')}"><span data-icon="eyeHidden"></span></button></div></div></div><p class="form-hint hidden" id="client-hint"></p></div></div>`;
}

function flareCardHtml(f) {
  return html`<div class="card"><div class="card-header"><h2 class="card-title">${t('integrations.flare')}</h2>${testButtonHtml('flaresolverr')}</div><div class="card-body">${testLineHtml('flaresolverr')}<div class="form-row"><div class="form-group"><label class="form-label" for="flare-url">${t('integrations.url')}</label><input type="text" class="input" id="flare-url" value="${f.url}"></div><div class="form-group"><label class="form-label" for="flare-timeout">${t('integrations.timeout')}</label><input type="number" class="input" id="flare-timeout" min="1" step="any" value="${(f.timeout_ms || 0) / 1000}"></div></div></div></div>`;
}

function proxyCardHtml(proxyParts) {
  const opt = function (value, key) {
    return html`<option value="${value}"${proxyParts.type === value ? ' selected' : ''}>${t(key)}</option>`;
  };
  return html`<div class="card int-advanced"><div class="card-header"><h2 class="card-title">${t('integrations.proxy')}</h2>${testButtonHtml('proxy')}</div><div class="card-body">${testLineHtml('proxy')}<div class="form-row"><div class="form-group"><label class="form-label" for="proxy-type">${t('integrations.proxyType')}</label><select class="select" id="proxy-type">${opt('http', 'integrations.proxyTypeHttp')}${opt('https', 'integrations.proxyTypeHttps')}${opt('socks5', 'integrations.proxyTypeSocks5')}</select></div><div class="form-group"><label class="form-label" for="proxy-download">${t('integrations.proxyAddress')}</label><input type="text" class="input" id="proxy-download" value="${proxyParts.address}" placeholder="${t('integrations.downloadProxyPlaceholder')}"></div></div></div></div>`;
}

function applyMirrorSourceType(el, type) {
  el.querySelector('#mirror-source-url-group').classList.toggle('hidden', type !== 'custom');
}

function applyNotifyChannelType(el, type) {
  // прячем, а не удаляем, чтобы набранный текст пережил переключения до сохранения
  closeNotifyEvents(false, true);
  el.querySelectorAll('.notify-channel').forEach(function (panel) {
    panel.classList.toggle('hidden', panel.getAttribute('data-channel') !== type);
  });
  const seg = el.querySelector('#notify-tabs');
  if (!seg) return;
  seg.querySelectorAll('.notify-tab').forEach(function (tab) {
    const on = tab.getAttribute('data-channel') === type;
    tab.classList.toggle('active', on);
    tab.setAttribute('aria-selected', on ? 'true' : 'false');
    tab.tabIndex = on ? 0 : -1;
  });
  updateSegmentThumb(seg);
}

function selectNotifyChannel(el, ch, focus) {
  notifyTabAuto = false;
  if (getNotifyChannelType() !== ch) {
    applyNotifyChannelType(el, ch);
    // проверка относится к вкладке, с которой ушли
    resetTest('int:notify');
    paintIntegrationTest('notify');
  }
  if (focus) el.querySelector('#notify-tab-' + ch).focus();
}

function applyEmailPortPlaceholder() {
  const sec = document.getElementById('notify-email-security');
  const port = document.getElementById('notify-email-port');
  if (sec && port) port.placeholder = String(EMAIL_DEFAULT_PORTS[sec.value] || 587);
}

function updateNotifyTabs(el) {
  const v = getNotifyFormValue();
  NOTIFY_CHANNELS.forEach(function (ch) {
    const tab = el.querySelector('#notify-tab-' + ch);
    if (tab) tab.classList.toggle('is-on', notifyChannelActive(v, ch));
    // незаполненный канал не работает, поэтому переключатель выключен и неактивен.
    // Сохраненное значение живет в data-saved и вернется, когда поля заполнят
    const box = el.querySelector('#notify-ch-enabled-' + ch);
    const filled = notifyChannelFilled(v, ch);
    if (box && !filled && !box.disabled) {
      box.dataset.saved = box.checked ? '1' : '';
      box.checked = false;
      box.disabled = true;
    } else if (box && filled && box.disabled) {
      box.disabled = false;
      box.checked = box.dataset.saved === '1';
    }
  });
}

function notifyChannelFilled(v, ch) {
  if (ch === 'browser') return browserDeviceCount() > 0;
  const c = v[ch];
  if (ch === 'telegram') return c.bot_token.trim() !== '';
  if (ch === 'webhook') return c.url.trim() !== '';
  if (ch === 'email') return c.host.trim() !== '' && c.from.trim() !== '' && c.to.trim() !== '';
  return c.app_token.trim() !== '' && c.user_key.trim() !== '';
}

function notifyChannelActive(v, ch) {
  return v.enabled && v[ch].enabled && v.events.length > 0 && notifyChannelFilled(v, ch);
}

function integrationTestMissing(kind) {
  const filled = function (id) { return document.getElementById(id).value.trim() !== ''; };
  if (kind === 'client') return filled('client-host') ? '' : t('integrations.testNeedsHost');
  if (kind === 'flaresolverr') return filled('flare-url') ? '' : t('integrations.testNeedsUrl');
  if (kind === 'proxy') return getProxyDownloadValue() ? '' : t('integrations.testNeedsProxy');
  if (kind === 'mirror_source') {
    const v = getMirrorSourceFormValue();
    return v.type !== 'custom' || v.url ? '' : t('integrations.testNeedsUrl');
  }
  if (getNotifyChannelType() === 'browser' && browserDeviceCount() === 0) return t('integrations.testNeedsBrowser');
  return notifyChannelFilled(getNotifyFormValue(), getNotifyChannelType()) ? '' : t('integrations.testNeedsNotify');
}

function paintIntegrationTest(kind) {
  const btn = document.querySelector('.btn-test-int[data-kind="' + kind + '"]');
  const line = document.querySelector('.test-result[data-kind="' + kind + '"]');
  if (!btn || !line) return;
  const key = btn.getAttribute('data-key');
  paintTestButton(btn, key, t('integrations.test'), function () { return integrationTestMissing(kind); });
  paintTestLine(line, key);
}

function initialNotifyChannel(n) {
  const tg = n.telegram || {};
  const wh = n.webhook || {};
  const em = n.email || {};
  const po = n.pushover || {};
  if (tg.bot_token) return 'telegram';
  if (wh.url) return 'webhook';
  if (em.host) return 'email';
  if (po.app_token || po.user_key) return 'pushover';
  return '';
}

function notifyEventsOf(ch) {
  return Array.isArray(ch.events) ? ch.events : NOTIFY_DEFAULT_EVENTS;
}

function notifyEventsCountText(n) {
  return t('integrations.notifyEventsCount', { n: n, total: NOTIFY_EVENTS.length });
}

function updateNotifyEventCounts(root) {
  root.querySelectorAll('.notify-events').forEach(function (box) {
    box.querySelector('.notify-events-count').textContent = notifyEventsCountText(box.querySelectorAll('input:checked').length);
  });
}

let openNotifyEvents = null;

function closeNotifyEvents(restoreFocus, force) {
  const box = openNotifyEvents;
  if (!box) return;
  openNotifyEvents = null;
  document.removeEventListener('pointerdown', onNotifyEventsPointerDown, true);
  document.removeEventListener('keydown', onNotifyEventsKeydown, true);
  window.removeEventListener('resize', positionNotifyEventsPanel);
  const panel = box.querySelector('.notify-events-panel');
  const finish = function () {
    box.classList.remove('open-up');
    panel.hidden = true;
    panel.style.maxHeight = '';
  };
  box.classList.remove('open');
  // панель скрывается только после анимации выхода; при смене вкладки родитель прячется сразу
  if (force) {
    cancelClose(box);
    finish();
  } else {
    closeAnimated(box, finish);
  }
  const trigger = box.querySelector('.notify-events-trigger');
  trigger.setAttribute('aria-expanded', 'false');
  if (restoreFocus) trigger.focus();
}

function notifyEventsGone(box) {
  // страницы при навигации только прячутся, а слушатели на document: невидимая панель не должна глотать Escape и клики
  return !box.isConnected || !box.getClientRects().length;
}

function onNotifyEventsPointerDown(e) {
  if (notifyEventsGone(openNotifyEvents)) {
    closeNotifyEvents(false, true);
    return;
  }
  // затемнение шторки закрывается по click: если скрыть его на pointerdown, клик уйдет в то, что под ним
  if (e.target.classList.contains('notify-events-backdrop')) return;
  if (!openNotifyEvents.contains(e.target)) closeNotifyEvents(false);
}

function onNotifyEventsKeydown(e) {
  if (e.key !== 'Escape') return;
  if (notifyEventsGone(openNotifyEvents)) {
    closeNotifyEvents(false, true);
    return;
  }
  e.preventDefault();
  e.stopPropagation();
  closeNotifyEvents(true);
}

function positionNotifyEventsPanel() {
  const box = openNotifyEvents;
  if (!box) return;
  if (notifyEventsGone(box)) {
    closeNotifyEvents(false, true);
    return;
  }
  const panel = box.querySelector('.notify-events-panel');
  box.classList.remove('open-up');
  panel.style.maxHeight = '';
  if (document.documentElement.classList.contains('mobile-layout')) return;
  // .content скроллится сам и режет панель: высота по видимому месту, вверх если снизу тесно
  const scroller = box.closest('.content');
  const bounds = scroller ? scroller.getBoundingClientRect() : { top: 0, bottom: window.innerHeight };
  const rect = box.getBoundingClientRect();
  const gap = 4 + 8;
  const below = Math.min(bounds.bottom, window.innerHeight) - rect.bottom - gap;
  const above = rect.top - Math.max(bounds.top, 0) - gap;
  const natural = panel.scrollHeight;
  if (natural <= below) return;
  const up = above > below;
  box.classList.toggle('open-up', up);
  panel.style.maxHeight = Math.max(up ? above : below, 120) + 'px';
}

function openNotifyEventsPanel(box) {
  closeNotifyEvents(false);
  openNotifyEvents = box;
  cancelClose(box);
  box.classList.add('open');
  const panel = box.querySelector('.notify-events-panel');
  panel.hidden = false;
  box.querySelector('.notify-events-trigger').setAttribute('aria-expanded', 'true');
  positionNotifyEventsPanel();
  document.addEventListener('pointerdown', onNotifyEventsPointerDown, true);
  document.addEventListener('keydown', onNotifyEventsKeydown, true);
  window.addEventListener('resize', positionNotifyEventsPanel);
  const first = panel.querySelector('input');
  if (first) first.focus({ preventScroll: true });
}

function onNotifyEventsClick(e) {
  const box = e.target.closest('.notify-events');
  if (!box) return;
  if (e.target.classList.contains('notify-events-backdrop')) {
    closeNotifyEvents(false);
  } else if (e.target.closest('.notify-events-done')) {
    closeNotifyEvents(true);
  } else if (e.target.closest('.notify-events-trigger')) {
    if (openNotifyEvents === box) closeNotifyEvents(true);
    else openNotifyEventsPanel(box);
  }
}

function notifyEventsHtml(channel, events) {
  // панель поверх карточки, а не в потоке: шесть тумблеров растягивали карточку и соседнюю тоже.
  // Подсказку внутри видно сразу, прятать ее в (i) незачем
  const panelId = 'notify-events-panel-' + channel;
  return html`<div class="notify-events"><div class="notify-events-row"><span class="form-label">${t('integrations.notifyEvents')}</span><span class="notify-events-count">${notifyEventsCountText(events.length)}</span><button type="button" class="btn btn-ghost btn-sm notify-events-trigger" aria-haspopup="dialog" aria-expanded="false" aria-controls="${panelId}">${t('integrations.notifyEventsEdit')}</button></div><div class="notify-events-backdrop"></div><div class="notify-events-panel" id="${panelId}" role="dialog" aria-label="${t('integrations.notifyEvents')}" hidden><p class="form-hint form-hint-keep">${t('integrations.notifyEventsHint')}</p>${NOTIFY_EVENTS.map(function (kind) {
    return html`<label class="form-switch"><input type="checkbox" id="notify-ev-${channel}-${kind}"${events.indexOf(kind) >= 0 ? ' checked' : ''}><span>${t(NOTIFY_EVENT_KEYS[kind])}</span></label>`;
  })}<div class="notify-events-footer"><button type="button" class="btn btn-primary btn-sm notify-events-done">${t('common.done')}</button></div></div></div>`;
}

function notifyPasswordHtml(id, value, field) {
  return html`<div class="input-password-wrap"><input id="${id}" data-reveal="/api/integrations/secret" data-reveal-field="${field}"${secureInputAttrs('notify-secret')} value="${value}"><button type="button" class="input-password-toggle" aria-label="${t('trackers.showCredentials')}"><span data-icon="eyeHidden"></span></button></div>`;
}

function mirrorConfirmHtml() {
  if (mirrorConfirm === null) return '';
  return html`<hr class="form-divider"><div class="form-group"><label class="form-switch"><input type="checkbox" id="mirror_confirm"${mirrorConfirm ? ' checked' : ''}><span>${t('settings.mirrorConfirm')}</span></label><p class="form-hint">${t('settings.mirrorConfirmHint')}</p></div>`;
}

function mirrorSourceCardHtml(ms) {
  const type = ms.type === 'jsdelivr' || ms.type === 'custom' ? ms.type : 'github';
  const opt = function (value, key) {
    return html`<option value="${value}"${type === value ? ' selected' : ''}>${t(key)}</option>`;
  };
  return html`<div class="card int-advanced"><div class="card-header"><h2 class="card-title">${t('integrations.mirrorSource')}</h2>${testButtonHtml('mirror_source')}</div><div class="card-body">${testLineHtml('mirror_source')}<div class="form-row"><div class="form-group"><label class="form-label" for="mirror-source-type">${t('integrations.mirrorSourceType')}</label><select class="select" id="mirror-source-type">${opt('github', 'integrations.mirrorSourceGithub')}${opt('jsdelivr', 'integrations.mirrorSourceJsdelivr')}${opt('custom', 'integrations.mirrorSourceCustom')}</select><p class="form-hint">${t('integrations.mirrorSourceHint')}</p></div><div class="form-group${type === 'custom' ? '' : ' hidden'}" id="mirror-source-url-group"><label class="form-label" for="mirror-source-url">${t('integrations.mirrorSourceUrl')}</label><input type="text" class="input" id="mirror-source-url" value="${ms.url || ''}" placeholder="https://example.com/{path}"><p class="form-hint">${t('integrations.mirrorSourceUrlHint')}</p></div><div class="form-group form-group-switch-end"><label class="form-switch"><input type="checkbox" id="mirror-source-direct"${ms.direct ? ' checked' : ''}><span>${t('integrations.mirrorSourceDirect')}</span></label></div></div>${mirrorConfirmHtml()}</div></div>`;
}

function notifyCardHtml(n, selected) {
  const tg = n.telegram || {};
  const wh = n.webhook || {};
  const em = n.email || {};
  const br = n.browser || {};
  const po = n.pushover || {};
  const security = em.security || 'starttls';
  const securityOptions = ['starttls', 'tls', 'none'].map(function (s) {
    return html`<option value="${s}"${security === s ? ' selected' : ''}>${t('integrations.notifyEmailSecurity.' + s)}</option>`;
  });
  const tabs = NOTIFY_CHANNELS.map(function (ch) {
    const on = selected === ch;
    return html`<button type="button" class="seg-ctrl-btn notify-tab${on ? ' active' : ''}" role="tab" id="notify-tab-${ch}" data-channel="${ch}" aria-selected="${on ? 'true' : 'false'}" aria-controls="notify-panel-${ch}" tabindex="${on ? '0' : '-1'}"><span class="notify-tab-name">${t(NOTIFY_CHANNEL_KEYS[ch])}</span><span class="notify-tab-dot" role="img" title="${t('integrations.notifyChannelActiveDot')}" aria-label="${t('integrations.notifyChannelActiveDot')}"></span></button>`;
  });
  const panel = function (ch, c, fields) {
    return html`<div class="notify-channel" data-channel="${ch}" role="tabpanel" id="notify-panel-${ch}" aria-labelledby="notify-tab-${ch}"><div class="form-group"><label class="form-switch"><input type="checkbox" id="notify-ch-enabled-${ch}"${c.enabled === false ? '' : ' checked'}><span>${t('integrations.notifyChannelEnabled')}</span></label></div>${fields}</div>`;
  };
  const telegram = html`<div class="form-group"><label class="form-label" for="notify-bot-token">${t('integrations.notifyBotToken')}</label>${notifyPasswordHtml('notify-bot-token', tg.bot_token, 'notifications.telegram.bot_token')}</div>`;
  const webhook = html`<div class="form-group"><label class="form-label" for="notify-webhook-url">${t('integrations.notifyWebhookUrl')}</label><input type="text" class="input" id="notify-webhook-url" autocomplete="off" value="${wh.url}"></div>`;
  const email = html`<div class="form-row int-row-3"><div class="form-group"><label class="form-label" for="notify-email-host">${t('integrations.notifyEmailHost')}</label><input type="text" class="input" id="notify-email-host" autocomplete="off" value="${em.host}"></div><div class="form-group"><label class="form-label" for="notify-email-port">${t('integrations.notifyEmailPort')}</label><input type="number" class="input" id="notify-email-port" min="0" max="65535" autocomplete="off" value="${em.port || ''}"></div><div class="form-group"><label class="form-label" for="notify-email-security">${t('integrations.notifyEmailSecurity')}</label><select class="select" id="notify-email-security">${securityOptions}</select></div></div><div class="form-row"><div class="form-group"><label class="form-label" for="notify-email-username">${t('integrations.notifyEmailUsername')}</label><input type="text" class="input" id="notify-email-username" autocomplete="off" value="${em.username}"><p class="form-hint">${t('integrations.notifyEmailAuthHint')}</p></div><div class="form-group"><label class="form-label" for="notify-email-password">${t('integrations.notifyEmailPassword')}</label>${notifyPasswordHtml('notify-email-password', em.password, 'notifications.email.password')}</div></div><div class="form-row"><div class="form-group"><label class="form-label" for="notify-email-from">${t('integrations.notifyEmailFrom')}</label><input type="text" class="input" id="notify-email-from" autocomplete="off" value="${em.from}"></div><div class="form-group"><label class="form-label" for="notify-email-to">${t('integrations.notifyEmailTo')}</label><input type="text" class="input" id="notify-email-to" autocomplete="off" value="${em.to}"><p class="form-hint">${t('integrations.notifyEmailToHint')}</p></div></div>`;
  const pushover = html`<div class="form-row"><div class="form-group"><label class="form-label" for="notify-pushover-token">${t('integrations.notifyPushoverToken')}</label>${notifyPasswordHtml('notify-pushover-token', po.app_token, 'notifications.pushover.app_token')}</div><div class="form-group"><label class="form-label" for="notify-pushover-user">${t('integrations.notifyPushoverUser')}</label>${notifyPasswordHtml('notify-pushover-user', po.user_key, 'notifications.pushover.user_key')}<p class="form-hint">${t('integrations.notifyPushoverHint')}</p></div></div>`;
  return html`<div class="card"><div class="card-header"><h2 class="card-title">${t('integrations.notify')}</h2>${testButtonHtml('notify')}</div><div class="card-body" id="notify-card-body" autocomplete="off">${testLineHtml('notify')}<div class="form-group"><label class="form-switch"><input type="checkbox" id="notify-enabled"${n.enabled ? ' checked' : ''}><span>${t('integrations.notifyEnabled')}</span></label></div><div class="form-group"><label class="form-label" for="notify-language">${t('integrations.notifyLanguage')}</label><select class="select" id="notify-language"><option value="ru"${n.language === 'ru' ? ' selected' : ''}>${t('settings.languageRu')}</option><option value="en"${n.language === 'ru' ? '' : ' selected'}>${t('settings.languageEn')}</option></select></div>${notifyEventsHtml('all', notifyEventsOf(n))}<div class="form-group"><span class="form-label" id="notify-tabs-label">${t('integrations.notifyChannels')}</span><div class="seg-ctrl seg-ctrl-fit notify-tabs" id="notify-tabs" role="tablist" aria-labelledby="notify-tabs-label"><div class="seg-ctrl-thumb" aria-hidden="true"></div>${tabs}</div></div><div class="notify-panels">${panel('telegram', tg, telegram)}${panel('webhook', wh, webhook)}${panel('email', em, email)}${panel('pushover', po, pushover)}${panel('browser', br, browserPanelHtml())}</div></div></div>`;
}

function metricsCardHtml(m) {
  return html`<div class="card int-advanced"><div class="card-header"><h2 class="card-title">${t('integrations.metrics')}</h2></div><div class="card-body" autocomplete="off"><div class="form-group"><label class="form-label" for="metrics-token">${t('integrations.metricsToken')}</label><div class="int-field-action">${notifyPasswordHtml('metrics-token', m.token || '', 'metrics.token')}<button type="button" class="btn btn-ghost" id="metrics-generate">${t('integrations.metricsGenerate')}</button></div><p class="form-hint">${t('integrations.metricsHint')}</p></div></div></div>`;
}

function applyClientHint(el, type) {
  const hintEl = el.querySelector('#client-hint');
  if (!hintEl) return;
  const hintKey = { transmission: 'integrations.transmissionHint', deluge: 'integrations.delugeHint', downloadstation: 'integrations.downloadstationHint' }[type];
  hintEl.textContent = hintKey ? t(hintKey) : '';
  hintEl.classList.toggle('hidden', !hintKey);
  // у Deluge Web UI вход только по паролю
  const userInput = el.querySelector('#client-username');
  if (userInput) userInput.closest('.form-group').classList.toggle('hidden', type === 'deluge');
}

function getSelectedClient() {
  const select = document.getElementById('download-client');
  return (select && select.value) || 'qbit';
}

function readClientFields() {
  const v = {
    host: document.getElementById('client-host').value,
    username: document.getElementById('client-username').value,
    password: document.getElementById('client-password').value,
  };
  if (clientDraftType === 'deluge') delete v.username;
  return v;
}

function writeClientFields(v) {
  document.getElementById('client-host').value = v.host || '';
  document.getElementById('client-username').value = v.username || '';
  document.getElementById('client-password').value = v.password || '';
}

function getNotifyChannelType() {
  const tab = document.querySelector('#notify-tabs .notify-tab.active');
  return (tab && tab.getAttribute('data-channel')) || 'telegram';
}


export function getIntegrationTestConfig(kind) {
  if (kind === 'qbit' || kind === 'transmission' || kind === 'deluge' || kind === 'downloadstation') {
    return readClientFields();
  }
  if (kind === 'flaresolverr') {
    return {
      url: document.getElementById('flare-url').value,
      timeout_ms: flareTimeoutMs(),
    };
  }
  if (kind === 'proxy') {
    return { download_proxy: getProxyDownloadValue() };
  }
  if (kind === 'mirror_source') {
    return getMirrorSourceFormValue();
  }
  if (kind === 'notify') {
    return Object.assign(getNotifyFormValue(), { channel: getNotifyChannelType() });
  }
  return {};
}

export function getMirrorSourceFormValue() {
  return {
    type: document.getElementById('mirror-source-type').value,
    url: document.getElementById('mirror-source-url').value.trim(),
    direct: document.getElementById('mirror-source-direct').checked,
  };
}

export function getMetricsFormValue() {
  return { token: document.getElementById('metrics-token').value.trim() };
}

export function getNotifyFormValue() {
  const val = function (id) { return document.getElementById(id).value; };
  const chEnabled = function (ch) {
    const box = document.getElementById('notify-ch-enabled-' + ch);
    return box.disabled ? box.dataset.saved === '1' : box.checked;
  };
  return {
    telegram: { enabled: chEnabled('telegram'), bot_token: val('notify-bot-token') },
    webhook: { enabled: chEnabled('webhook'), url: val('notify-webhook-url') },
    email: {
      enabled: chEnabled('email'),
      host: val('notify-email-host'),
      port: parseInt(val('notify-email-port'), 10) || 0,
      security: val('notify-email-security'),
      username: val('notify-email-username'),
      password: val('notify-email-password'),
      from: val('notify-email-from'),
      to: val('notify-email-to'),
    },
    pushover: { enabled: chEnabled('pushover'), app_token: val('notify-pushover-token'), user_key: val('notify-pushover-user') },
    browser: { enabled: chEnabled('browser') },
    events: NOTIFY_EVENTS.filter(function (kind) { return document.getElementById('notify-ev-all-' + kind).checked; }),
    enabled: document.getElementById('notify-enabled').checked,
    language: val('notify-language'),
  };
}

export function buildIntegrationsDoc(sections) {
  // остальное берем из снимка сервера, не из формы: соседняя секция с неподтвержденной
  // правкой (напр. переключением клиента) не должна уехать вместе с этой
  const base = integrationsCache ? structuredClone(integrationsCache) : {};
  sections.forEach(function (name) { INT_SECTIONS[name].apply(base, INT_SECTIONS[name].read()); });
  return base;
}
