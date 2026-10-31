import { hydrateIcons } from './icons.js';
import { html } from './util.js';

let tour = null;

export function isTourOpen() {
  return tour !== null;
}

export function startTour(steps, opts) {
  if (tour || !steps || !steps.length) return false;
  const first = steps[0].target();
  if (!first || !first.isConnected) return false;

  const layer = document.createElement('div');
  layer.className = 'tour-layer';
  layer.innerHTML = html`<div class="tour-catcher"></div><div class="tour-spot"></div><div class="tour-pop" role="dialog" aria-modal="true" aria-labelledby="tour-title" aria-describedby="tour-text"><button type="button" class="btn btn-ghost btn-sm btn-icon tour-close" aria-label="${t('tour.close')}" title="${t('tour.close')}"><span data-icon="x"></span></button><div class="tour-title" id="tour-title"></div><div class="tour-text" id="tour-text"></div><div class="tour-foot"><span class="tour-count"></span><button type="button" class="btn btn-primary tour-next"></button></div></div>`.toString();
  hydrateIcons(layer);

  const catcher = layer.querySelector('.tour-catcher');
  const spot = layer.querySelector('.tour-spot');
  const pop = layer.querySelector('.tour-pop');
  const closeBtn = layer.querySelector('.tour-close');
  const nextBtn = layer.querySelector('.tour-next');
  const active = document.activeElement;

  const state = {
    steps: steps,
    idx: 0,
    onClose: opts && opts.onClose,
    restoreTo: active && active !== document.body ? active : null,
    cleanups: [],
    raf: 0,
    animTimer: 0,
    observer: null,
  };
  tour = state;

  function on(target, type, fn, options) {
    target.addEventListener(type, fn, options);
    state.cleanups.push(function () { target.removeEventListener(type, fn, options); });
  }

  // CSS zoom на .app: Chrome и Firefox отдают getBoundingClientRect в координатах окна, старые
  // движки нет. Умножаем только если реальная ширина .app расходится с --page-width
  function zoomFactor(target) {
    const app = target.closest('.app.page-scaled');
    if (!app) return 1;
    const style = getComputedStyle(document.documentElement);
    const scale = parseFloat(style.getPropertyValue('--page-scale')) || 1;
    if (scale === 1) return 1;
    const pageW = parseFloat(style.getPropertyValue('--page-width')) || 360;
    const w = app.getBoundingClientRect().width;
    return Math.abs(w - pageW) < Math.abs(w - pageW * scale) ? scale : 1;
  }

  function finish(reason) {
    if (tour !== state) return;
    tour = null;
    cancelAnimationFrame(state.raf);
    clearTimeout(state.animTimer);
    if (state.observer) state.observer.disconnect();
    state.cleanups.forEach(function (fn) { fn(); });
    layer.remove();
    const back = state.restoreTo;
    if (back && back.isConnected && typeof back.focus === 'function') back.focus({ preventScroll: true });
    if (state.onClose) state.onClose(reason);
  }

  function currentTarget() {
    const target = state.steps[state.idx].target();
    if (!target || !target.isConnected || target.getClientRects().length === 0) return null;
    return target;
  }

  function place() {
    state.raf = 0;
    const target = currentTarget();
    if (!target) {
      finish('aborted');
      return;
    }
    const k = zoomFactor(target);
    const r = target.getBoundingClientRect();
    const pad = 6;
    const sTop = r.top * k - pad;
    const sLeft = r.left * k - pad;
    const sW = r.width * k + pad * 2;
    const sH = r.height * k + pad * 2;
    spot.style.top = sTop + 'px';
    spot.style.left = sLeft + 'px';
    spot.style.width = sW + 'px';
    spot.style.height = sH + 'px';

    const vw = document.documentElement.clientWidth;
    const vh = window.innerHeight;
    const margin = 12;
    const pw = pop.offsetWidth;
    const ph = pop.offsetHeight;
    let top = sTop + sH + 10;
    if (top + ph > vh - margin) top = sTop - 10 - ph;
    top = Math.max(margin, Math.min(top, vh - ph - margin));
    const left = Math.max(margin, Math.min(sLeft + sW / 2 - pw / 2, vw - pw - margin));
    pop.style.top = top + 'px';
    pop.style.left = left + 'px';
  }

  function schedule() {
    if (!state.raf) state.raf = requestAnimationFrame(place);
  }

  function watchTarget(target) {
    if (state.observer) state.observer.disconnect();
    if (typeof ResizeObserver === 'undefined') return;
    if (!state.observer) state.observer = new ResizeObserver(schedule);
    state.observer.observe(target.closest('[id^="view-"]') || document.body);
  }

  function show(moved) {
    const target = currentTarget();
    if (!target) {
      finish('aborted');
      return;
    }
    const step = state.steps[state.idx];
    const last = state.idx === state.steps.length - 1;
    layer.querySelector('.tour-title').textContent = step.title;
    layer.querySelector('.tour-text').textContent = step.text;
    layer.querySelector('.tour-count').textContent = t('tour.counter', { n: state.idx + 1, total: state.steps.length });
    nextBtn.textContent = last ? t('tour.done') : t('tour.next');
    if (moved) {
      // переход между шагами плавный, а скролл и ресайз двигают рамку без задержки
      layer.classList.add('is-moving');
      clearTimeout(state.animTimer);
      state.animTimer = setTimeout(function () { layer.classList.remove('is-moving'); }, 260);
    }
    target.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'auto' });
    watchTarget(target);
    state.raf = requestAnimationFrame(place);
  }

  on(catcher, 'click', function (e) { e.preventDefault(); e.stopPropagation(); });
  on(catcher, 'contextmenu', function (e) { e.preventDefault(); });
  // без preventDefault нажатие по затемнению увело бы фокус из окна
  on(catcher, 'mousedown', function (e) { e.preventDefault(); });
  on(closeBtn, 'click', function () { finish('closed'); });
  on(nextBtn, 'click', function () {
    if (state.idx >= state.steps.length - 1) {
      finish('done');
      return;
    }
    state.idx++;
    show(true);
  });
  on(document, 'keydown', function (e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      finish('closed');
      return;
    }
    if (e.key !== 'Tab') return;
    const items = [closeBtn, nextBtn];
    const at = items.indexOf(document.activeElement);
    e.preventDefault();
    if (at === -1) {
      (e.shiftKey ? nextBtn : closeBtn).focus({ preventScroll: true });
      return;
    }
    items[(at + (e.shiftKey ? items.length - 1 : 1)) % items.length].focus({ preventScroll: true });
  }, true);
  on(window, 'hashchange', function () { finish('aborted'); });
  on(window, 'resize', schedule);
  on(window, 'scroll', schedule, true);
  if (window.visualViewport) {
    on(window.visualViewport, 'resize', schedule);
    on(window.visualViewport, 'scroll', schedule);
  }

  document.body.appendChild(layer);
  show(false);
  nextBtn.focus({ preventScroll: true });
  return true;
}
