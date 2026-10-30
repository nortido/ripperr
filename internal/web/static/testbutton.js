import { showToast } from './api.js';
import { hydrateIcons } from './icons.js';
import { html } from './util.js';

// результат висит до наведения, нажатия или правки формы, успех гаснет и сам.
// Состояние по ключу, чтобы кнопки строки, диалога и карточки показывали одно и то же
const results = new Map();
const running = new Map();

export function resetTest(key) {
  if (running.has(key) || !results.has(key)) return false;
  results.delete(key);
  return true;
}

export function forgetTest(key) {
  results.delete(key);
}

export function abortTest(key, origin) {
  const run = running.get(key);
  if (!run || (origin && run.origin !== origin)) return;
  run.controller.stoppedByUser = true;
  run.controller.abort();
}

export function paintTestButton(btn, key, idleTitle, missing) {
  const isRunning = running.has(key);
  const result = isRunning ? null : results.get(key);
  let name = 'activity';
  let title = idleTitle;
  let disabled = false;
  if (isRunning) {
    name = 'loader';
    title = t('trackers.testStop');
  } else if (result) {
    name = result.ok ? 'check' : 'xCircle';
    title = result.label;
  } else {
    const reason = missing();
    if (reason) {
      disabled = true;
      title = reason;
    }
  }
  btn.disabled = disabled;
  btn.classList.toggle('is-spinning', isRunning);
  btn.classList.toggle('btn-test-ok', !!result && result.ok);
  btn.classList.toggle('btn-test-fail', !!result && !result.ok);
  btn.setAttribute('title', title);
  btn.setAttribute('aria-label', title);
  if (btn.dataset.paint === name) return;
  btn.dataset.paint = name;
  btn.innerHTML = html`<span data-icon="${name}"></span>`;
  hydrateIcons(btn);
}

export function paintTestLine(el, key) {
  const result = running.has(key) ? null : results.get(key);
  const text = result && !result.ok ? result.label : '';
  el.textContent = text;
  el.hidden = !text;
}

export function bindTestButton(btn, key, onClick, onReset) {
  btn.addEventListener('click', function () {
    onClick();
  });
  btn.addEventListener('pointerenter', function (e) {
    // на телефоне наведения нет, там результат снимает нажатие
    if (e.pointerType === 'touch') return;
    if (resetTest(key)) onReset();
  });
}

function expireOk(key, result, repaint) {
  const clear = function () {
    if (results.get(key) !== result) return;
    results.delete(key);
    repaint();
  };
  if (window.matchMedia('(hover: none)').matches) {
    setTimeout(clear, 10000);
    return;
  }
  // отсчет с первого движения мыши: пока ее не трогают, человек мог отойти и не увидеть итог
  document.addEventListener('pointermove', function () { setTimeout(clear, 5000); }, { once: true });
}

export async function runTest(key, request, opts) {
  // повторное нажатие отменяет проверку: обрыв запроса снимает контекст на сервере
  if (running.has(key)) {
    abortTest(key);
    return;
  }
  // клик по показанному результату только убирает его, новый тест со следующего
  if (resetTest(key)) {
    opts.repaint();
    return;
  }
  const controller = new AbortController();
  running.set(key, { controller: controller, origin: opts.origin });
  opts.repaint();
  const timeoutId = opts.timeoutMs ? setTimeout(function () { controller.abort(); }, opts.timeoutMs) : 0;
  let res;
  try {
    res = await request(controller.signal);
  } catch (err) {
    res = { ok: false, error: err.name === 'AbortError' ? '' : err.message };
  }
  clearTimeout(timeoutId);
  running.delete(key);
  if (controller.stoppedByUser) {
    results.delete(key);
    opts.repaint();
    return;
  }
  const label = res.ok ? t('trackers.testOk') : (res.error || t('trackers.testFail'));
  const result = { ok: !!res.ok, label: label };
  results.set(key, result);
  if (result.ok) expireOk(key, result, opts.repaint);
  opts.repaint();
  if (opts.toast && !res.ok) showToast(label, 'error');
}
