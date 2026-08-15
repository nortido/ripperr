export const historyState = { offset: 0, limit: 10, status: '', ignored: false, sort: 'checked', name: '', tracker: 0, category: '' };

export function isMobileStackedLayout() {
  return document.documentElement.classList.contains('mobile-layout');
}

// lets histfilter.js dismiss the custom-select and name-filter overlays that
// live in components.js without importing that file directly, which would
// create an ES module cycle (components.js already imports from histfilter.js)
export const overlayBridge = {
  getOpenCustomSelect: null,
  closeCustomSelect: null,
  closeNameFilter: null,
};
