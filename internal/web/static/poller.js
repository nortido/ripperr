import { currentView } from './router.js';

// один поллер на вид: таймеры идут только пока этот вид открыт, а вкладка
// видима; задачи с пометкой onResume срабатывают ещё и в момент возврата вкладки
export function createPoller(view, tasks) {
  let timers = [];
  let bound = false;

  function clearTimers() {
    timers.forEach(clearInterval);
    timers = [];
  }

  function onVisibility() {
    if (currentView !== view) return;
    if (document.visibilityState !== 'visible') {
      clearTimers();
      return;
    }
    tasks.forEach(function (task) {
      if (task.onResume) task.run();
    });
    start();
  }

  function start() {
    if (currentView !== view || document.visibilityState !== 'visible') return;
    if (!timers.length) {
      timers = tasks.map(function (task) { return setInterval(task.run, task.ms); });
    }
    if (!bound) {
      document.addEventListener('visibilitychange', onVisibility);
      bound = true;
    }
  }

  function stop() {
    clearTimers();
    if (bound) {
      document.removeEventListener('visibilitychange', onVisibility);
      bound = false;
    }
  }

  return { start: start, stop: stop };
}
