import { bindLogout, loadAuthState } from './auth.js';
import { initGlobalDismiss } from './components.js';
import { hydrateIcons } from './icons.js';
import { initAdvanced, initLayout, syncThemeColor } from './layout.js';
import { initRouter, parseHash, showView } from './router.js';
import { historyState } from './state.js';
import { loadStoredPageSize } from './util.js';

initAdvanced();
syncThemeColor();
// глобальная, как и t(): i18n.js обычный скрипт, login.html грузит его сам
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
