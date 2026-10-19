import { closeAnimated } from './components.js';

let active = null;

export function confirmDialog(opts) {
  if (active) active.finish(false, true);
  return new Promise(function (resolve) {
    const prevFocus = document.activeElement;
    const textId = 'confirm-text';
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
    foot.append(cancelBtn, okBtn);
    box.append(text, foot);
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
      active = null;
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
      if (e.key !== 'Escape') return;
      e.preventDefault();
      e.stopPropagation();
      finish(false);
    }
    active = { finish: finish };

    cancelBtn.addEventListener('click', function () { finish(false); });
    okBtn.addEventListener('click', function () { finish(true); });
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
