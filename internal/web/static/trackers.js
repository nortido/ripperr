import { fetchJSON, showToast } from './api.js';
import { initChipInput, initPasswordToggles } from './components.js';
import { hydrateIcons, icon } from './icons.js';
import { integrationsCache, setIntegrationsCache } from './integrations.js';
import { initCustomSelects } from './select.js';
import { html, randomFieldName, secureInputAttrs, trackerDisplayDomain } from './util.js';

export let openTrackerKey = null;

export function activeDomainOptionsHtml(domain, mirrors, selected) {
  const sel = selected || '';
  const parts = [];
  parts.push(html`<option value=""${sel === '' ? ' selected' : ''}>${t('trackers.activeDomainAuto')}</option>`);
  if (domain) {
    parts.push(html`<option value="${domain}"${sel === domain ? ' selected' : ''}>${domain}</option>`);
  }
  (mirrors || []).forEach(function (m) {
    if (!m || m === domain) return;
    parts.push(html`<option value="${m}"${sel === m ? ' selected' : ''}>${m}</option>`);
  });
  return html`${parts}`;
}

export function syncActiveDomainSelect(card, domain, mirrors) {
  const select = card.querySelector('.tr-active-domain');
  if (!select) return;
  const prev = select.value;
  const mirrorSet = mirrors || [];
  let next = prev;
  if (prev && prev !== '' && prev !== domain && mirrorSet.indexOf(prev) === -1) next = '';
  select.innerHTML = activeDomainOptionsHtml(domain, mirrorSet, next);
  select.value = next;
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
    cookies: tracker.cookies || '',
    publicity: tracker.publicity || 'public',
    mirrors: (tracker.mirrors || []).slice(),
    active_domain: tracker.active_domain || '',
  };
}

export function gatherTrackerConfig(item, mirrorValues) {
  return {
    enabled: item.querySelector('.tr-enabled').checked,
    domain: item.querySelector('.tr-domain').value,
    hour_limit: parseInt(item.querySelector('.tr-hour-limit').value, 10) || 0,
    proxy_type: parseInt(item.querySelector('.tr-proxy-type').value, 10),
    login_url: item.querySelector('.tr-login-url').value,
    download_url: item.querySelector('.tr-download-url').value,
    credentials: item.querySelector('.input-password-wrap .input').value,
    cookies: item.querySelector('.tr-cookies').value,
    publicity: item.querySelector('.tr-publicity').value,
    mirrors: (mirrorValues || []).slice(),
    active_domain: item.querySelector('.tr-active-domain').value,
  };
}

export function isTrackerDirty(item, mirrorValues) {
  if (!item._trackerSaved) return false;
  const cur = gatherTrackerConfig(item, mirrorValues);
  const saved = item._trackerSaved;
  return JSON.stringify(cur) !== JSON.stringify(saved);
}

export function trackerItemHtml(tracker) {
  const pub = tracker.publicity || 'public';
  const key = tracker.key;
  const mirrors = tracker.mirrors || [];
  const isOpen = openTrackerKey === key;
  const disabledCls = tracker.enabled ? '' : ' tracker-item-disabled';
  const domain = trackerDisplayDomain(tracker);
  return html`<div class="tracker-item${disabledCls}${isOpen ? ' is-open' : ''}" data-key="${key}"><div class="tracker-row" role="button" tabindex="0" aria-expanded="${isOpen ? 'true' : 'false'}"><label class="form-switch tracker-row-toggle" title="${t('trackers.enabled')}"><input class="tr-enabled" type="checkbox"${tracker.enabled ? ' checked' : ''}><span class="sr-only">${t('trackers.enabled')}</span></label><button type="button" class="btn btn-ghost btn-icon-only btn-test-tracker" data-key="${key}" title="${t('trackers.test')}" aria-label="${t('trackers.test')}"><span data-icon="activity"></span></button><div class="tracker-row-main"><span class="tracker-row-name">${tracker.name || key}</span>${domain ? html`<span class="tracker-row-domain">${domain}</span>` : ''}</div><span class="tracker-chevron" data-icon="chevron"></span></div><div class="tracker-panel"><div class="tracker-panel-inner"><div class="tracker-form form-row"><div class="form-group"><label class="form-label">${t('trackers.domain')}</label><input type="text" class="input tr-domain" autocomplete="off" name="${randomFieldName('tr-domain')}" value="${tracker.domain}"></div><div class="form-group"><label class="form-label">${t('trackers.hourLimit')}</label><input type="number" class="input tr-hour-limit" min="1" value="${tracker.hour_limit}"></div><div class="form-group"><label class="form-label">${t('trackers.proxy')}</label><select class="select tr-proxy-type"><option value="0"${tracker.proxy_type === 0 ? ' selected' : ''}>${t('trackers.proxyDirect')}</option><option value="1"${tracker.proxy_type === 1 ? ' selected' : ''}>${t('trackers.proxyFlare')}</option></select></div><div class="form-group"><label class="form-label">${t('trackers.publicity')}</label><select class="select tr-publicity"><option value="public"${pub === 'public' ? ' selected' : ''}>${t('trackers.public')}</option><option value="private"${pub === 'private' ? ' selected' : ''}>${t('trackers.private')}</option></select></div><div class="form-group form-group-full"><label class="form-label">${t('trackers.mirrors')}</label><div class="chip-input tr-mirrors"><div class="chip-input-chips"></div><input type="text" class="chip-input-field" autocomplete="off" name="${randomFieldName('tr-mirror')}" placeholder="${t('trackers.mirrorsPlaceholder')}"></div></div><div class="form-group form-group-full"><label class="form-label" for="tr-active-domain-${key}">${t('trackers.activeDomain')}</label><select class="select tr-active-domain" id="tr-active-domain-${key}">${activeDomainOptionsHtml(tracker.domain, mirrors, tracker.active_domain || '')}</select><p class="form-hint">${t('trackers.activeDomainHint')}</p></div><div class="form-group form-group-full"><label class="form-label">${t('trackers.loginUrl')}</label><input type="text" class="input tr-login-url" autocomplete="off" name="${randomFieldName('tr-login')}" value="${tracker.login_url}"></div><div class="form-group form-group-full"><label class="form-label">${t('trackers.downloadUrl')}</label><input type="text" class="input tr-download-url" autocomplete="off" name="${randomFieldName('tr-dl')}" value="${tracker.download_url}"></div><div class="form-group form-group-full"><label class="form-label">${t('trackers.credentials')}</label><div class="input-password-wrap"><input${secureInputAttrs('tr-cred')} value="${tracker.credentials}"><button type="button" class="input-password-toggle" aria-label="${t('trackers.showCredentials')}"><span data-icon="eye"></span></button></div></div><div class="form-group form-group-full"><label class="form-label">${t('trackers.cookies')}</label><input type="text" class="input tr-cookies" autocomplete="off" name="${randomFieldName('tr-cookie')}" value="${tracker.cookies}"></div></div><div class="form-submit-row tracker-save-row"><button type="button" class="btn btn-primary btn-save-tracker">${t('common.save')}</button></div></div></div></div>`;
}


export function setTrackerItemEnabled(item, enabled) {
  item.classList.toggle('tracker-item-disabled', !enabled);
}

export function toggleTrackerItem(item, key) {
  const wasOpen = item.classList.contains('is-open');
  item.closest('.tracker-list').querySelectorAll('.tracker-item.is-open').forEach(function (other) {
    if (other !== item) {
      other.classList.remove('is-open');
      const row = other.querySelector('.tracker-row');
      if (row) row.setAttribute('aria-expanded', 'false');
    }
  });
  if (!wasOpen) {
    item.classList.add('is-open');
    openTrackerKey = key;
    const row = item.querySelector('.tracker-row');
    if (row) row.setAttribute('aria-expanded', 'true');
    initCustomSelects(item);
    initPasswordToggles(item);
    hydrateIcons(item);
  } else {
    item.classList.remove('is-open');
    openTrackerKey = null;
    const row = item.querySelector('.tracker-row');
    if (row) row.setAttribute('aria-expanded', 'false');
  }
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

export async function testTracker(key, btn, item, mirrorValues) {
  if (!btn || btn.disabled) return;
  const defaultLabel = t('trackers.test');
  btn.disabled = true;
  btn.classList.add('is-spinning');
  const prevHtml = btn.innerHTML;
  btn.innerHTML = '<span data-icon="refresh"></span>';
  btn.setAttribute('title', defaultLabel);
  btn.setAttribute('aria-label', defaultLabel);
  hydrateIcons(btn);
  let ok = false;
  let errMsg = '';
  const controller = new AbortController();
  const timeoutMs = trackerTestTimeoutMs(item);
  const timeoutId = setTimeout(function () { controller.abort(); }, timeoutMs);
  try {
    const opts = { method: 'POST', signal: controller.signal };
    if (item && item.classList.contains('is-open') && isTrackerDirty(item, mirrorValues)) {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body = JSON.stringify({ config: gatherTrackerConfig(item, mirrorValues) });
    }
    const res = await fetch('/api/trackers/' + encodeURIComponent(key) + '/test', opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* ignore */ }
    }
    if (res.status === 404) {
      errMsg = (data && data.error) ? data.error : 'HTTP 404';
    } else if (!res.ok) {
      errMsg = (data && data.error) ? data.error : 'HTTP ' + res.status;
    } else if (data && data.ok) {
      ok = true;
    } else {
      errMsg = (data && data.error) ? data.error : t('trackers.testFail');
    }
  } catch (err) {
    errMsg = err.name === 'AbortError' ? t('trackers.testFail') : err.message;
  }
  clearTimeout(timeoutId);
  btn.classList.remove('is-spinning');
  btn.disabled = false;
  if (ok) {
    const okLabel = t('trackers.testOk');
    btn.classList.add('btn-test-ok');
    btn.classList.remove('btn-test-fail');
    btn.innerHTML = '<span data-icon="check"></span>';
    btn.setAttribute('title', okLabel);
    btn.setAttribute('aria-label', okLabel);
    showToast(okLabel, 'success');
  } else {
    const failLabel = errMsg || t('trackers.testFail');
    btn.classList.add('btn-test-fail');
    btn.classList.remove('btn-test-ok');
    btn.innerHTML = '<span data-icon="xCircle"></span>';
    btn.setAttribute('title', failLabel);
    btn.setAttribute('aria-label', failLabel);
    showToast(failLabel, 'error');
  }
  hydrateIcons(btn);
  setTimeout(function () {
    btn.classList.remove('btn-test-ok', 'btn-test-fail');
    btn.innerHTML = prevHtml;
    btn.setAttribute('title', defaultLabel);
    btn.setAttribute('aria-label', defaultLabel);
    hydrateIcons(btn);
  }, 4000);
}

export async function loadTrackers() {
  const el = document.getElementById('view-trackers');
  el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
  try {
    const fetches = [fetchJSON('/api/trackers')];
    if (!integrationsCache) fetches.push(fetchJSON('/api/integrations'));
    const results = await Promise.all(fetches);
    if (!integrationsCache && results[1]) setIntegrationsCache(results[1]);
    renderTrackers(el, results[0] || []);
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export function renderTrackers(el, trackers) {
  let list;
  if (trackers.length === 0) {
    list = html`<p class="text-muted">${t('trackers.empty')}</p>`;
  } else {
    const items = trackers.map(function (tr) { return trackerItemHtml(tr); });
    list = html`<div class="tracker-list">${items}</div>`;
  }

  el.innerHTML = html`<div class="page-header"><h1 class="page-title">${t('trackers.title')}</h1></div>${list}`;

  const mirrorInputs = {};
  el.querySelectorAll('.tracker-item').forEach(function (item) {
    const key = item.getAttribute('data-key');
    const chipEl = item.querySelector('.tr-mirrors');
    const tracker = trackers.find(function (t) { return t.key === key; });
    if (tracker) item._trackerSaved = trackerConfigFromApi(tracker);
    mirrorInputs[key] = initChipInput(
      chipEl,
      null,
      (tracker && tracker.mirrors) || [],
      null,
      function (mirrors) {
        syncActiveDomainSelect(item, item.querySelector('.tr-domain').value, mirrors);
      }
    );
    const domainInput = item.querySelector('.tr-domain');
    if (domainInput) {
      domainInput.addEventListener('input', function () {
        syncActiveDomainSelect(item, domainInput.value, mirrorInputs[key].getValues());
      });
    }

    const row = item.querySelector('.tracker-row');
    if (row) {
      row.addEventListener('click', function (e) {
        if (e.target.closest('.tracker-row-toggle') || e.target.closest('.btn-test-tracker')) return;
        toggleTrackerItem(item, key);
      });
      row.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggleTrackerItem(item, key);
        }
      });
    }
  });

  el.querySelectorAll('.btn-save-tracker').forEach(function (btn) {
    btn.addEventListener('click', async function () {
      const item = btn.closest('.tracker-item');
      const key = item.getAttribute('data-key');
      const body = {
        enabled: item.querySelector('.tr-enabled').checked,
        domain: item.querySelector('.tr-domain').value,
        hour_limit: parseInt(item.querySelector('.tr-hour-limit').value, 10) || 0,
        proxy_type: parseInt(item.querySelector('.tr-proxy-type').value, 10),
        login_url: item.querySelector('.tr-login-url').value,
        download_url: item.querySelector('.tr-download-url').value,
        credentials: item.querySelector('.input-password-wrap .input').value,
        cookies: item.querySelector('.tr-cookies').value,
        publicity: item.querySelector('.tr-publicity').value,
        mirrors: mirrorInputs[key].getValues(),
        active_domain: item.querySelector('.tr-active-domain').value,
      };
      btn.disabled = true;
      try {
        await fetchJSON('/api/trackers/' + encodeURIComponent(key), {
          method: 'PUT',
          body: JSON.stringify(body),
        });
        showToast(t('trackers.saved', { key: key }), 'success');
        item._trackerSaved = body;
        const domainEl = item.querySelector('.tracker-row-domain');
        if (domainEl) {
          const active = body.active_domain || body.domain || '';
          domainEl.textContent = active;
          domainEl.style.display = active ? '' : 'none';
        }
      } catch (err) {
        showToast(err.message, 'error');
      }
      btn.disabled = false;
    });
  });

  el.querySelectorAll('.tr-enabled').forEach(function (checkbox) {
    checkbox.addEventListener('change', async function (e) {
      e.stopPropagation();
      const item = checkbox.closest('.tracker-item');
      const key = item.getAttribute('data-key');
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
  });

  el.querySelectorAll('.btn-test-tracker').forEach(function (btn) {
    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      const item = btn.closest('.tracker-item');
      const key = btn.getAttribute('data-key');
      testTracker(key, btn, item, mirrorInputs[key] ? mirrorInputs[key].getValues() : []);
    });
  });

  el.querySelectorAll('.tracker-row-toggle').forEach(function (label) {
    label.addEventListener('click', function (e) { e.stopPropagation(); });
  });

  if (openTrackerKey) {
    const openItem = el.querySelector('.tracker-item[data-key="' + CSS.escape(openTrackerKey) + '"]');
    if (openItem) {
      initCustomSelects(openItem);
      initPasswordToggles(openItem);
    }
  }

  initPasswordToggles(el);
  hydrateIcons(el);
  initCustomSelects(el);
}


