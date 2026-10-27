import { closeAnimated } from './components.js';

let stack = [];
let dialogSeq = 0;

export function confirmDialog(opts) {
  stack.slice().forEach(function (d) { d.finish(false, true); });
  return new Promise(function (resolve) {
    const prevFocus = document.activeElement;
    const textId = 'confirm-text-' + (++dialogSeq);
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay confirm-overlay';
    const box = document.createElement('div');
    box.className = 'modal confirm-modal';
    box.setAttribute('role', 'alertdialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-describedby', textId);
    const text = document.createElement('p');
    text.className = 'confirm-text';
    text.id = textId;
    text.textContent = opts.text;
    const foot = document.createElement('div');
    foot.className = 'modal-foot';
    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'btn btn-ghost';
    cancelBtn.textContent = t('common.cancel');
    const okBtn = document.createElement('button');
    okBtn.type = 'button';
    okBtn.className = 'btn ' + (opts.danger ? 'btn-danger' : 'btn-primary');
    okBtn.textContent = opts.confirmLabel;
    box.append(text);
    let checkEl = null;
    if (opts.checkLabel) {
      const wrap = document.createElement('div');
      wrap.className = 'confirm-check';
      const sw = document.createElement('label');
      sw.className = 'form-switch';
      checkEl = document.createElement('input');
      checkEl.type = 'checkbox';
      checkEl.disabled = !!opts.checkDisabled;
      const checkText = document.createElement('span');
      checkText.textContent = opts.checkLabel;
      sw.append(checkEl, checkText);
      wrap.append(sw);
      if (opts.checkHint) {
        const hint = document.createElement('p');
        hint.className = 'form-hint';
        hint.id = textId + '-hint';
        hint.textContent = opts.checkHint;
        checkEl.setAttribute('aria-describedby', hint.id);
        wrap.append(hint);
      }
      box.append(wrap);
    }
    foot.append(cancelBtn, okBtn);
    box.append(foot);
    overlay.appendChild(box);

    // inert ставим только там, где его еще не было, и снимаем только свой
    const root = document.documentElement;
    const inerted = [];
    document.querySelectorAll('.app, body > .modal-overlay').forEach(function (el) {
      if (!el.inert) {
        el.inert = true;
        inerted.push(el);
      }
    });
    const addedModalOpen = !root.classList.contains('modal-open');
    if (addedModalOpen) root.classList.add('modal-open');

    let done = false;
    function finish(result, force) {
      if (done) return;
      done = true;
      stack = stack.filter(function (d) { return d !== entry; });
      // window в фазе перехвата получает Escape раньше диалога трекера и глобального обработчика
      window.removeEventListener('keydown', onKey, true);
      if (force) {
        overlay.remove();
      } else {
        // остальной откат синхронный, только DOM уходит после анимации; inert не пускает Tab на уходящую подложку
        overlay.inert = true;
        closeAnimated(overlay, function () { overlay.remove(); });
      }
      inerted.forEach(function (el) { el.inert = false; });
      if (addedModalOpen) root.classList.remove('modal-open');
      if (prevFocus && prevFocus.isConnected && typeof prevFocus.focus === 'function') prevFocus.focus();
      resolve(result);
    }
    function onKey(e) {
      if (e.key !== 'Escape' || stack[stack.length - 1] !== entry) return;
      e.preventDefault();
      e.stopPropagation();
      finish(false);
    }
    const entry = { finish: finish, text: false };
    stack.push(entry);

    cancelBtn.addEventListener('click', function () { finish(false); });
    okBtn.addEventListener('click', function () { finish(checkEl ? { checked: checkEl.checked } : true); });
    // как у диалога трекера: подложка закрывает, только если и нажатие, и отпускание были на ней
    // второй клик быстрого двойного по кнопке, открывшей вопрос, попадает уже на подложку
    // и сразу отменял бы его. Первые 300 мс подложку не слушаем, Escape и "Отмена" работают
    const openedAt = performance.now();
    let downOnBackdrop = false;
    overlay.addEventListener('pointerdown', function (e) {
      downOnBackdrop = e.target === overlay && performance.now() - openedAt >= 300;
    });
    overlay.addEventListener('click', function (e) {
      if (e.target === overlay && downOnBackdrop) finish(false);
      downOnBackdrop = false;
    });
    window.addEventListener('keydown', onKey, true);

    document.body.appendChild(overlay);
    (opts.danger ? cancelBtn : okBtn).focus();
  });
}

export async function copyText(text) {
  // clipboard API есть только в secure context, а приложение обычно открывают по http в локалке
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:fixed;top:-1000px;opacity:0';
  document.body.appendChild(ta);
  ta.select();
  const ok = document.execCommand('copy');
  ta.remove();
  if (!ok) throw new Error(t('logs.copyFail'));
}

export function textDialog(content, opts) {
  stack.filter(function (d) { return d.text; }).forEach(function (d) { d.finish(false, true); });
  return new Promise(function (resolve) {
    const prevFocus = document.activeElement;
    const backTo = opts && opts.returnFocus;
    const textId = 'confirm-text-' + (++dialogSeq);
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay confirm-overlay';
    const box = document.createElement('div');
    box.className = 'modal confirm-modal';
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-describedby', textId);
    const text = document.createElement('p');
    text.className = 'confirm-text toast-details-text';
    text.id = textId;
    text.textContent = content;
    const foot = document.createElement('div');
    foot.className = 'modal-foot';
    const copyBtn = document.createElement('button');
    copyBtn.type = 'button';
    copyBtn.className = 'btn btn-ghost';
    copyBtn.textContent = t('toast.copy');
    const closeBtn = document.createElement('button');
    closeBtn.type = 'button';
    closeBtn.className = 'btn btn-primary';
    closeBtn.textContent = t('common.close');
    foot.append(copyBtn, closeBtn);
    box.append(text, foot);
    overlay.appendChild(box);

    const root = document.documentElement;
    const inerted = [];
    document.querySelectorAll('.app, body > .modal-overlay').forEach(function (el) {
      if (!el.inert) {
        el.inert = true;
        inerted.push(el);
      }
    });
    const addedModalOpen = !root.classList.contains('modal-open');
    if (addedModalOpen) root.classList.add('modal-open');

    let done = false;
    let copyTimer = null;
    function finish(result, force) {
      if (done) return;
      done = true;
      stack = stack.filter(function (d) { return d !== entry; });
      clearTimeout(copyTimer);
      window.removeEventListener('keydown', onKey, true);
      if (force) {
        overlay.remove();
      } else {
        overlay.inert = true;
        closeAnimated(overlay, function () { overlay.remove(); });
      }
      inerted.forEach(function (el) { el.inert = false; });
      if (addedModalOpen) root.classList.remove('modal-open');
      const back = backTo && backTo.isConnected ? backTo : prevFocus;
      if (back && back.isConnected && typeof back.focus === 'function') back.focus();
      resolve(result);
    }
    function onKey(e) {
      if (e.key !== 'Escape' || stack[stack.length - 1] !== entry) return;
      e.preventDefault();
      e.stopPropagation();
      finish(false);
    }
    const entry = { finish: finish, text: true };
    stack.push(entry);

    closeBtn.addEventListener('click', function () { finish(false); });
    copyBtn.addEventListener('click', async function () {
      let label;
      try {
        await copyText(content);
        label = t('toast.copied');
      } catch (_) {
        label = t('logs.copyFail');
      }
      if (done) return;
      copyBtn.textContent = label;
      clearTimeout(copyTimer);
      copyTimer = setTimeout(function () { copyBtn.textContent = t('toast.copy'); }, 1500);
    });
    const openedAt = performance.now();
    let downOnBackdrop = false;
    overlay.addEventListener('pointerdown', function (e) {
      downOnBackdrop = e.target === overlay && performance.now() - openedAt >= 300;
    });
    overlay.addEventListener('click', function (e) {
      if (e.target === overlay && downOnBackdrop) finish(false);
      downOnBackdrop = false;
    });
    window.addEventListener('keydown', onKey, true);

    document.body.appendChild(overlay);
    closeBtn.focus();
  });
}
