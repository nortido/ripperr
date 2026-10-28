import { fetchJSON, showToast } from './api.js';
import { setSaving } from './components.js';
import { hydrateIcons } from './icons.js';
import { formatDateTime, html } from './util.js';

const DEVICE_KEY = 'ripperr-push-device';

let box = null;
let notify = function () {};
let st = { key: '', devices: [], thisId: '', error: '' };

export function browserDeviceCount() {
  return st.devices.length;
}

export function browserPanelHtml() {
  return html`<div class="form-group notify-browser"><span class="form-label">${t('integrations.notifyBrowserDevices')}</span><div id="notify-browser-body"></div><p class="form-hint">${t('integrations.notifyBrowserHint')}</p></div>`;
}

export function initBrowserPush(root, onChange) {
  box = root.querySelector('#notify-browser-body');
  if (!box) return;
  notify = onChange;
  st = { key: '', devices: [], thisId: '', error: '' };
  box.addEventListener('click', onClick);
  load();
}

export function reloadBrowserDevices() {
  // тест удаляет устройства с истекшей подпиской
  if (box && box.isConnected) load();
}

function storedDevice() {
  try {
    return JSON.parse(localStorage.getItem(DEVICE_KEY)) || null;
  } catch (_) {
    return null;
  }
}

function isIOS() {
  return /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

function isStandalone() {
  return navigator.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
}

function pushSupported() {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
}

function b64urlToBytes(s) {
  const pad = '='.repeat((4 - (s.length % 4)) % 4);
  const bin = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from(bin, function (c) { return c.charCodeAt(0); });
}

function sameBytes(buf, bytes) {
  if (!buf) return false;
  const a = new Uint8Array(buf);
  return a.length === bytes.length && a.every(function (v, i) { return v === bytes[i]; });
}

async function pushSubscription() {
  const reg = await navigator.serviceWorker.getRegistration('/');
  return reg ? reg.pushManager.getSubscription() : null;
}

async function detectThisDevice() {
  const dev = storedDevice();
  if (!dev || !pushSupported()) return '';
  try {
    const sub = await pushSubscription();
    if (sub && sub.endpoint === dev.endpoint && st.devices.some(function (d) { return d.id === dev.id; })) return dev.id;
  } catch (_) { /* нет подписки, значит это устройство не включено */ }
  return '';
}

async function load() {
  const mine = box;
  try {
    const data = await fetchJSON('/api/push');
    st.key = data.public_key || '';
    st.devices = data.devices || [];
    st.error = '';
  } catch (err) {
    st.error = err.message;
  }
  st.thisId = await detectThisDevice();
  // страницу могли перерисовать, пока шел запрос
  if (box !== mine || !mine.isConnected) return;
  render();
  notify();
}

function status() {
  if (!window.isSecureContext) return { text: t('integrations.notifyBrowserNeedHttps') };
  if (isIOS() && !isStandalone()) return { text: t('integrations.notifyBrowserIosHome') };
  if (!pushSupported()) return { text: t('integrations.notifyBrowserUnsupported') };
  if (Notification.permission === 'denied') return { text: t('integrations.notifyBrowserBlocked') };
  if (st.thisId) return { text: t('integrations.notifyBrowserOnHere') };
  if (!st.key) return { text: t('integrations.notifyBrowserLoadFail', { error: st.error }) };
  return { text: t('integrations.notifyBrowserOffHere'), button: true };
}

function render() {
  const s = status();
  const rows = st.devices.map(function (d) {
    const label = d.label || t('integrations.notifyBrowserUnknown');
    const here = d.id === st.thisId ? html` <span class="notify-device-here">${t('integrations.notifyBrowserThis')}</span>` : '';
    return html`<li class="notify-device"><div class="notify-device-info"><span class="notify-device-name">${label}${here}</span><span class="notify-device-date">${formatDateTime(new Date(d.created_at * 1000).toISOString())}</span></div><button type="button" class="btn btn-ghost btn-icon-only notify-device-del" data-id="${d.id}" title="${t('common.remove')}" aria-label="${t('integrations.notifyBrowserRemove', { name: label })}"><span data-icon="trash"></span></button></li>`;
  });
  const list = st.devices.length
    ? html`<ul class="notify-devices">${rows}</ul>`
    : html`<p class="form-hint form-hint-keep">${t('integrations.notifyBrowserEmpty')}</p>`;
  box.innerHTML = html`<p class="form-hint form-hint-keep" role="status">${s.text}</p>${s.button ? html`<button type="button" class="btn btn-ghost notify-browser-enable">${t('integrations.notifyBrowserEnable')}</button>` : ''}${list}`;
  hydrateIcons(box);
}

function onClick(e) {
  const del = e.target.closest('.notify-device-del');
  if (del) {
    removeDevice(del);
    return;
  }
  const enable = e.target.closest('.notify-browser-enable');
  if (enable) enableHere(enable);
}

async function enableHere(btn) {
  setSaving(btn, true);
  try {
    // первый await: iOS и Safari принимают запрос только прямо из жеста
    const perm = await Notification.requestPermission();
    if (perm !== 'granted') {
      showToast(t('integrations.notifyBrowserDenied'), 'error');
      render();
      return;
    }
    await navigator.serviceWorker.register('/sw.js');
    const reg = await navigator.serviceWorker.ready;
    const key = b64urlToBytes(st.key);
    let sub = await reg.pushManager.getSubscription();
    // подписка под другим ключом сервера (ключ пересоздан) не доставит ничего
    if (sub && !sameBytes(sub.options.applicationServerKey, key)) {
      await sub.unsubscribe();
      sub = null;
    }
    if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
    const dev = await fetchJSON('/api/push/devices', { method: 'POST', body: JSON.stringify(Object.assign(sub.toJSON(), { origin: location.origin })) });
    localStorage.setItem(DEVICE_KEY, JSON.stringify({ id: dev.id, endpoint: sub.endpoint }));
    await load();
  } catch (err) {
    showToast(err.message, 'error');
    render();
  }
}

async function removeDevice(btn) {
  const id = btn.getAttribute('data-id');
  setSaving(btn, true);
  try {
    await fetchJSON('/api/push/devices/' + encodeURIComponent(id), { method: 'DELETE' });
    const dev = storedDevice();
    if (id === st.thisId || (dev && dev.id === id)) {
      try {
        const sub = await pushSubscription();
        if (sub) await sub.unsubscribe();
      } catch (_) { /* сервер уже забыл устройство, браузерная подписка не критична */ }
      localStorage.removeItem(DEVICE_KEY);
    }
    await load();
  } catch (err) {
    setSaving(btn, false);
    showToast(err.message, 'error');
  }
}
