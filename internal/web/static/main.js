import { bindLogout, loadAuthState } from './auth.js';
import { initGlobalDismiss } from './components.js';
import { hydrateIcons } from './icons.js';
import { initLayout } from './layout.js';
import { initRouter, parseHash, showView } from './router.js';
import { historyState } from './state.js';
import { loadStoredPageSize } from './util.js';

// i18n.js остаётся обычным скриптом: login.html грузит его сам, поэтому t() и
// applyStaticI18n() здесь глобальные, а не импортируемые.

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
