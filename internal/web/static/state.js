export const historyState = { offset: 0, limit: 10, status: '', ignored: false, sort: 'checked', name: '', tracker: 0, category: '' };

export function historyFiltered() {
  return !!(historyState.status || historyState.ignored || historyState.name ||
    historyState.tracker || historyState.category);
}

export function isMobileStackedLayout() {
  return document.documentElement.classList.contains('mobile-layout');
}

// через этот мост histfilter.js закрывает оверлеи select.js и namefilter.js.
// Прямой импорт дал бы цикл модулей: оба сами импортируют histfilter.js
export const overlayBridge = {
  getOpenCustomSelect: null,
  closeCustomSelect: null,
  closeNameFilter: null,
};
