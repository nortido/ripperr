export const historyState = { offset: 0, limit: 10, status: '', ignored: false, sort: 'checked', name: '', tracker: 0, category: '' };

const HISTORY_PAGE_KEY = 'ripperr-history-page';

// страница запоминается вместе с фильтрами: с другими фильтрами это другой список
function historyPageSignature() {
  return [historyState.sort, historyState.status, historyState.ignored ? 1 : 0, historyState.name,
    historyState.tracker, historyState.category].join('|');
}

export function loadHistoryOffset() {
  try {
    const saved = JSON.parse(localStorage.getItem(HISTORY_PAGE_KEY) || 'null');
    if (saved && saved.sig === historyPageSignature() && saved.offset > 0) return saved.offset;
  } catch (_) { /* localStorage бывает недоступен */ }
  return 0;
}

export function saveHistoryOffset() {
  try {
    localStorage.setItem(HISTORY_PAGE_KEY, JSON.stringify({ sig: historyPageSignature(), offset: historyState.offset }));
  } catch (_) { /* localStorage бывает недоступен */ }
}

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
