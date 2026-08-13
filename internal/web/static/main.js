import { bindLogout, loadAuthState } from './auth.js';
import { initGlobalDismiss } from './components.js';
import { hydrateIcons } from './icons.js';
import { initLayout, initTheme } from './layout.js';
import { initRouter, parseHash, showView } from './router.js';
import { historyState } from './state.js';
import { loadStoredPageSize } from './util.js';

// i18n.js stays a classic script: login.html loads it on its own, so t() and
// applyStaticI18n() are globals here rather than imports.

initTheme();
applyStaticI18n();
hydrateIcons(document);
initLayout();
initGlobalDismiss();
initRouter();

const initialView = parseHash();
historyState.limit = loadStoredPageSize(10);
showView(initialView, false);
bindLogout();
loadAuthState();
