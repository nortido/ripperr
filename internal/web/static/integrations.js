import { fetchJSON, showToast } from './api.js';
import { initCustomSelects, initPasswordToggles } from './components.js';
import { hydrateIcons, icon } from './icons.js';
import { html, secureInputAttrs } from './util.js';

export let integrationsCache = null;

// trackers.js fills the cache too, and a module can only assign its own bindings
export function setIntegrationsCache(doc) {
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

export function getProxyDownloadValue() {
  const typeEl = document.getElementById('proxy-type');
  const addrEl = document.getElementById('proxy-download');
  if (!typeEl || !addrEl) return '';
  return buildDownloadProxy(typeEl.value, addrEl.value);
}


export async function loadIntegrations() {
  const el = document.getElementById('view-integrations');
  el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
  try {
    integrationsCache = await fetchJSON('/api/integrations');
    renderIntegrations(el, integrationsCache);
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export function renderIntegrations(el, data) {
  const q = data.qbit || {};
  const f = data.flaresolverr || {};
  const p = data.proxy || {};
  const proxyParts = parseDownloadProxy(p.download_proxy || '');

  el.innerHTML = html`<div class="page-header"><h1 class="page-title">${t('integrations.title')}</h1></div><div class="grid-2"><div class="card"><div class="card-header"><h2 class="card-title">${t('integrations.qbit')}</h2></div><div class="card-body" autocomplete="off"><div class="form-group"><label class="form-label">${t('integrations.host')}</label><input type="text" class="input" id="qbit-host" autocomplete="off" name="qb-host-field" value="${q.host}"></div><div class="form-group"><label class="form-label">${t('integrations.username')}</label><input type="text" class="input" id="qbit-username" autocomplete="off" name="qb-user-field" value="${q.username}"></div><div class="form-group"><label class="form-label">${t('integrations.password')}</label><div class="input-password-wrap"><input id="qbit-password"${secureInputAttrs('qb-secret')} value="${q.password}"><button type="button" class="input-password-toggle" aria-label="${t('trackers.showCredentials')}"><span data-icon="eye"></span></button></div></div><div class="btn-group"><button type="button" class="btn btn-primary btn-save-int" data-section="qbit">${t('common.save')}</button><button type="button" class="btn btn-ghost btn-test-int" data-kind="qbit">${t('integrations.test')}</button><span class="test-result" data-kind="qbit"></span></div></div></div><div class="card"><div class="card-header"><h2 class="card-title">${t('integrations.flare')}</h2></div><div class="card-body"><div class="form-group"><label class="form-label">${t('integrations.url')}</label><input type="text" class="input" id="flare-url" value="${f.url}"></div><div class="form-group"><label class="form-label">${t('integrations.timeout')}</label><input type="number" class="input" id="flare-timeout" min="1" value="${f.timeout_ms || 0}"></div><div class="btn-group"><button type="button" class="btn btn-primary btn-save-int" data-section="flaresolverr">${t('common.save')}</button><button type="button" class="btn btn-ghost btn-test-int" data-kind="flaresolverr">${t('integrations.test')}</button><span class="test-result" data-kind="flaresolverr"></span></div></div></div><div class="card"><div class="card-header"><h2 class="card-title">${t('integrations.proxy')}</h2></div><div class="card-body"><div class="proxy-type-row"><div class="form-group"><label class="form-label" for="proxy-type">${t('integrations.proxyType')}</label><select class="select" id="proxy-type"><option value="http"${proxyParts.type === 'http' ? ' selected' : ''}>${t('integrations.proxyTypeHttp')}</option><option value="https"${proxyParts.type === 'https' ? ' selected' : ''}>${t('integrations.proxyTypeHttps')}</option><option value="socks5"${proxyParts.type === 'socks5' ? ' selected' : ''}>${t('integrations.proxyTypeSocks5')}</option></select></div><div class="form-group"><label class="form-label" for="proxy-download">${t('integrations.proxyAddress')}</label><input type="text" class="input" id="proxy-download" value="${proxyParts.address}" placeholder="${t('integrations.downloadProxyPlaceholder')}"></div></div><div class="btn-group"><button type="button" class="btn btn-primary btn-save-int" data-section="proxy">${t('common.save')}</button><button type="button" class="btn btn-ghost btn-test-int" data-kind="proxy">${t('integrations.test')}</button><span class="test-result" data-kind="proxy"></span></div></div></div></div>`;

  el.querySelectorAll('.btn-save-int').forEach(function (btn) {
    btn.addEventListener('click', async function () {
      const section = btn.getAttribute('data-section');
      const doc = buildIntegrationsDoc();
      if (section === 'qbit') {
        doc.qbit = {
          host: document.getElementById('qbit-host').value,
          username: document.getElementById('qbit-username').value,
          password: document.getElementById('qbit-password').value,
        };
      } else if (section === 'flaresolverr') {
        doc.flaresolverr = {
          url: document.getElementById('flare-url').value,
          timeout_ms: parseInt(document.getElementById('flare-timeout').value, 10) || 0,
        };
      } else if (section === 'proxy') {
        doc.proxy = { download_proxy: getProxyDownloadValue() };
      }
      btn.disabled = true;
      try {
        await fetchJSON('/api/integrations', { method: 'PUT', body: JSON.stringify(doc) });
        integrationsCache = doc;
        showToast(t('integrations.saved'), 'success');
      } catch (err) {
        showToast(err.message, 'error');
      }
      btn.disabled = false;
    });
  });

  el.querySelectorAll('.btn-test-int').forEach(function (btn) {
    btn.addEventListener('click', async function () {
      const kind = btn.getAttribute('data-kind');
      const resultEl = el.querySelector('.test-result[data-kind="' + kind + '"]');
      resultEl.innerHTML = html`<span class="text-muted">${t('integrations.testing')}</span>`;
      btn.disabled = true;
      try {
        const config = getIntegrationTestConfig(kind);
        const res = await fetchJSON('/api/integrations/test', {
          method: 'POST',
          body: JSON.stringify({ kind: kind, config: config }),
        });
        if (res.ok) {
          resultEl.innerHTML = html`<span class="text-success">${icon('check')} ${t('integrations.ok')}</span>`;
          hydrateIcons(resultEl);
        } else {
          resultEl.innerHTML = html`<span class="text-danger">${res.error || t('integrations.error')}</span>`;
        }
      } catch (err) {
        resultEl.innerHTML = html`<span class="text-danger">${err.message}</span>`;
      }
      btn.disabled = false;
    });
  });

  initCustomSelects(el);
  initPasswordToggles(el);
  hydrateIcons(el);
}


export function getIntegrationTestConfig(kind) {
  if (kind === 'qbit') {
    return {
      host: document.getElementById('qbit-host').value,
      username: document.getElementById('qbit-username').value,
      password: document.getElementById('qbit-password').value,
    };
  }
  if (kind === 'flaresolverr') {
    return {
      url: document.getElementById('flare-url').value,
      timeout_ms: parseInt(document.getElementById('flare-timeout').value, 10) || 0,
    };
  }
  if (kind === 'proxy') {
    return { download_proxy: getProxyDownloadValue() };
  }
  return {};
}

export function buildIntegrationsDoc() {
  const base = integrationsCache ? JSON.parse(JSON.stringify(integrationsCache)) : {};
  if (!base.qbit) base.qbit = {};
  if (!base.flaresolverr) base.flaresolverr = {};
  if (!base.proxy) base.proxy = {};
  base.qbit.host = document.getElementById('qbit-host').value;
  base.qbit.username = document.getElementById('qbit-username').value;
  base.qbit.password = document.getElementById('qbit-password').value;
  base.flaresolverr.url = document.getElementById('flare-url').value;
  base.flaresolverr.timeout_ms = parseInt(document.getElementById('flare-timeout').value, 10) || 0;
  base.proxy.download_proxy = getProxyDownloadValue();
  return base;
}

// the view, filters and paging all live in the hash, so the whole location is
// carried over and restored after login

