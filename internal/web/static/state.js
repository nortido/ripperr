export const historyState = { offset: 0, limit: 10, status: '', ignored: false, sort: 'checked', name: '', tracker: 0, category: '' };

export function historyFiltered() {
  return !!(historyState.status || historyState.ignored || historyState.name ||
    historyState.tracker || historyState.category);
}

export function isMobileStackedLayout() {
  return document.documentElement.classList.contains('mobile-layout');
}

// позволяет histfilter.js закрывать оверлеи кастомного селекта (select.js) и
// фильтра по имени (namefilter.js) без прямого импорта этих файлов, так как оба
// уже импортируют из histfilter.js, и обратный прямой импорт создал бы
// цикл ES-модулей
export const overlayBridge = {
  getOpenCustomSelect: null,
  closeCustomSelect: null,
  closeNameFilter: null,
};
