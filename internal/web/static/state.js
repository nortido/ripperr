export const historyState = { offset: 0, limit: 10, status: '', ignored: false, sort: 'checked', name: '', tracker: 0, category: '' };

export function isMobileStackedLayout() {
  return document.documentElement.classList.contains('mobile-layout');
}

// lets histfilter.js dismiss the custom-select (select.js) and name-filter
// (namefilter.js) overlays without importing those files directly, since both
// of them already import from histfilter.js and a direct import back would
// create an ES module cycle
export const overlayBridge = {
  getOpenCustomSelect: null,
  closeCustomSelect: null,
  closeNameFilter: null,
};
