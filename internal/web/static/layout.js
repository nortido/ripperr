import { syncNameFilterLayout } from './namefilter.js';

export const THEME_KEY = 'ripperr-theme';
export const SIDEBAR_COLLAPSED_KEY = 'ripperr-sidebar-collapsed';
export const PAGE_SCALE_BASE = 360;
// масштаб ограничен только сверху: выше 1 на 880px выходил zoom 1.83 и заголовок в 44px.
// Снизу нельзя, макет на 360px не тянется и влезает в экран только при w/360.
// С базой 480px на iPhone (393px) все ужималось до 82%, текст 12px читался как 10
export const PAGE_SCALE_MAX = 1;
export const MOBILE_LAYOUT_MAX = 900;
export let layoutWasMobile = false;

export const ADVANCED_KEY = 'ripperr-advanced';

export function applyAdvanced(on) {
  // признак на корне, а поля прячет CSS: трекеры не перерисовываем,
  // и несохраненные правки в скрытых полях целы
  if (on) document.documentElement.setAttribute('data-advanced', '1');
  else document.documentElement.removeAttribute('data-advanced');
  try {
    localStorage.setItem(ADVANCED_KEY, on ? '1' : '0');
  } catch (_) { /* localStorage бывает недоступен */ }
}

export function advancedEnabled() {
  return document.documentElement.getAttribute('data-advanced') === '1';
}

export function initAdvanced() {
  let on = false;
  try {
    on = localStorage.getItem(ADVANCED_KEY) === '1';
  } catch (_) { /* localStorage бывает недоступен */ }
  if (on) document.documentElement.setAttribute('data-advanced', '1');
}

export const THEMES = ['dark', 'light', 'oled'];
const THEME_COLORS = { dark: '#0a0d12', oled: '#000000', light: '#f3f4f6' };

export function syncThemeColor() {
  // полосу статуса iPhone и вкладку браузера красит meta, CSS туда не достает
  const color = THEME_COLORS[document.documentElement.getAttribute('data-theme')];
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta && color) meta.setAttribute('content', color);
}

export function applyTheme(theme) {
  const t = THEMES.includes(theme) ? theme : 'dark';
  document.documentElement.setAttribute('data-theme', t);
  syncThemeColor();
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch (_) { /* localStorage бывает недоступен */ }
}

export function loadSidebarCollapsedPref() {
  try {
    const v = localStorage.getItem(SIDEBAR_COLLAPSED_KEY);
    if (v === null) return null;
    return v === '1';
  } catch (_) {
    return null;
  }
}

export function saveSidebarCollapsedPref(collapsed) {
  try {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? '1' : '0');
  } catch (_) { /* localStorage бывает недоступен */ }
}

export function layoutWidth() {
  // не innerWidth: на iOS он меняется при пинч-зуме и шлет resize, и масштаб
  // пересчитывался прямо под пальцами. clientWidth зум не трогает
  return document.documentElement.clientWidth || window.innerWidth;
}

export const SIDEBAR_WIDE_MIN = 1200;
export let sidebarOverlayOpen = false;

export function isSidebarOverlayWidth() {
  const w = layoutWidth();
  return w >= 901 && w < SIDEBAR_WIDE_MIN;
}

export function effectiveSidebarCollapsed() {
  if (layoutWidth() <= 900) return false;
  if (layoutWidth() < SIDEBAR_WIDE_MIN) return true;
  const pref = loadSidebarCollapsedPref();
  return pref !== null ? pref : false;
}

export function closeSidebarOverlay() {
  sidebarOverlayOpen = false;
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (sidebar) sidebar.classList.remove('sidebar-overlay-open');
  if (backdrop && isSidebarOverlayWidth()) {
    backdrop.classList.remove('visible');
    backdrop.hidden = true;
  }
  refreshSidebarChrome();
}

export function openSidebarOverlay() {
  if (!isSidebarOverlayWidth()) return;
  sidebarOverlayOpen = true;
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (sidebar) sidebar.classList.add('sidebar-overlay-open');
  if (backdrop) {
    backdrop.hidden = false;
    requestAnimationFrame(function () { backdrop.classList.add('visible'); });
  }
  refreshSidebarChrome();
}

export function refreshSidebarChrome() {
  const app = document.querySelector('.app');
  const collapsed = app && app.classList.contains('sidebar-collapsed');
  const btn = document.getElementById('sidebar-collapse-btn');
  const overlay = isSidebarOverlayWidth() && sidebarOverlayOpen;
  const hideLabels = collapsed && !overlay;
  if (btn) {
    const label = t(hideLabels ? 'nav.sidebarExpand' : 'nav.sidebarCollapse');
    btn.setAttribute('aria-label', label);
    btn.title = label + (/Mac|iPhone|iPad/.test(navigator.platform) ? ' (⌘B)' : ' (Ctrl+B)');
  }
  document.querySelectorAll('.sidebar-nav .nav-link, .sidebar-footer .nav-link').forEach(function (link) {
    const label = link.querySelector('.nav-label');
    if (!label) return;
    if (hideLabels) link.setAttribute('title', label.textContent);
    else link.removeAttribute('title');
  });
}

export function applySidebarCollapsed(collapsed) {
  const sidebar = document.getElementById('sidebar');
  const app = document.querySelector('.app');
  const btn = document.getElementById('sidebar-collapse-btn');
  if (sidebar) sidebar.classList.toggle('sidebar-collapsed', collapsed);
  if (app) app.classList.toggle('sidebar-collapsed', collapsed);
  if (btn) btn.setAttribute('aria-pressed', collapsed ? 'true' : 'false');
  if (!collapsed) closeSidebarOverlay();
  refreshSidebarChrome();
}

export function refreshSidebarLayout() {
  if (!isSidebarOverlayWidth()) closeSidebarOverlay();
  applySidebarCollapsed(effectiveSidebarCollapsed());
}

function toggleSidebar() {
  const w = layoutWidth();
  if (w <= 900) return;
  if (w < SIDEBAR_WIDE_MIN) {
    if (sidebarOverlayOpen) closeSidebarOverlay();
    else openSidebarOverlay();
    return;
  }
  const app = document.querySelector('.app');
  const next = !(app && app.classList.contains('sidebar-collapsed'));
  saveSidebarCollapsedPref(next);
  applySidebarCollapsed(next);
}

export function initSidebarCollapse() {
  const btn = document.getElementById('sidebar-collapse-btn');
  if (btn) btn.addEventListener('click', toggleSidebar);
  document.addEventListener('keydown', function (e) {
    // по коду клавиши, а не по символу: в русской раскладке там "и"
    if (e.code !== 'KeyB' || !(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) return;
    const target = e.target;
    if (target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))) return;
    e.preventDefault();
    toggleSidebar();
  });
  document.querySelectorAll('.sidebar-nav .nav-link').forEach(function (link) {
    link.addEventListener('click', function () {
      if (sidebarOverlayOpen) closeSidebarOverlay();
    });
  });
  refreshSidebarLayout();
}

export function applyMobileLayout() {
  const app = document.querySelector('.app');
  if (!app) return;
  const w = layoutWidth();
  const mobile = w > 0 && w < MOBILE_LAYOUT_MAX;
  const layoutChanged = mobile !== layoutWasMobile;
  if (mobile) {
    const scale = w / PAGE_SCALE_BASE;
    document.documentElement.classList.add('mobile-layout');
    if (scale < PAGE_SCALE_MAX) {
      app.classList.add('page-scaled');
      // на корне, чтобы значение пережило перерисовку .app
      document.documentElement.style.setProperty('--page-scale', String(scale));
    } else {
      app.classList.remove('page-scaled');
      document.documentElement.style.removeProperty('--page-scale');
    }
  } else {
    document.documentElement.classList.remove('mobile-layout');
    app.classList.remove('page-scaled');
    document.documentElement.style.removeProperty('--page-scale');
  }
  layoutWasMobile = mobile;
  refreshSidebarLayout();
  if (layoutChanged) syncNameFilterLayout();
}

export function syncSidebarToWidth() {
  // в том же кадре, что и resize: через rAF панель сначала мелькнула бы развернутой.
  // Идет до придушенного applyMobileLayout
  const mobile = layoutWidth() > 0 && layoutWidth() < MOBILE_LAYOUT_MAX;
  const sidebar = mobile !== layoutWasMobile ? document.getElementById('sidebar') : null;
  if (sidebar) sidebar.classList.add('no-anim');
  refreshSidebarLayout();
  if (sidebar) {
    void sidebar.offsetWidth;
    requestAnimationFrame(function () { sidebar.classList.remove('no-anim'); });
  }
}

export let mobileLayoutFrameScheduled = false;

export function closeMobileMenu() {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  const btn = document.getElementById('mobile-menu-btn');
  if (sidebar) sidebar.classList.remove('open');
  if (backdrop) {
    backdrop.classList.remove('visible');
    backdrop.hidden = true;
  }
  if (btn) btn.setAttribute('aria-expanded', 'false');
}

export function openMobileMenu() {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  const btn = document.getElementById('mobile-menu-btn');
  if (sidebar) sidebar.classList.add('open');
  if (backdrop) {
    backdrop.hidden = false;
    requestAnimationFrame(function () { backdrop.classList.add('visible'); });
  }
  if (btn) btn.setAttribute('aria-expanded', 'true');
}

const HEADER_SHRINK_SCROLL = 48;

function syncCompactHeader() {
  const p = Math.min(1, Math.max(0, window.scrollY / HEADER_SHRINK_SCROLL));
  const root = document.documentElement;
  root.style.setProperty('--hdr-p', p.toFixed(3));
  root.classList.toggle('header-compact', p >= 1);
}

// Копии кнопок, а не перенос самих узлов: виды перерисовывают шапку через
// innerHTML, и перенесенная кнопка осталась бы оторванной от страницы
function initMobileHeaderActions() {
  const bar = document.getElementById('mobile-header-actions');
  const header = document.querySelector('.mobile-header');
  const content = document.querySelector('.content');
  if (!bar || !header || !content) return;
  let originals = new Map();
  let frame = 0;

  function currentActions() {
    return document.querySelector('.view:not(.hidden) .page-header-actions');
  }

  function rebuild() {
    const actions = currentActions();
    const next = new Map();
    const nodes = [];
    if (actions) {
      actions.querySelectorAll('button:not(.hint-toggle)').forEach(function (b) {
        const c = b.cloneNode(true);
        c.removeAttribute('id');
        c.classList.add('btn-sm');
        next.set(c, b);
        nodes.push(c);
      });
    }
    bar.replaceChildren(...nodes);
    originals = next;
    syncVisibility();
  }

  // по самим кнопкам: у трекеров блок действий display: contents и без размеров
  function syncVisibility() {
    let bottom = -Infinity;
    originals.forEach(function (b) {
      if (b.offsetParent) bottom = Math.max(bottom, b.getBoundingClientRect().bottom);
    });
    const show = document.documentElement.classList.contains('mobile-layout') &&
      bottom > -Infinity && bottom <= header.getBoundingClientRect().bottom;
    bar.hidden = !show;
    header.classList.toggle('has-actions', show);
  }

  function schedule() {
    if (frame) return;
    frame = requestAnimationFrame(function () {
      frame = 0;
      rebuild();
    });
  }

  bar.addEventListener('click', function (e) {
    const c = e.target.closest('button');
    const b = c && originals.get(c);
    if (b && b.isConnected && !b.disabled) b.click();
  });
  new MutationObserver(schedule).observe(content, {
    subtree: true,
    childList: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['class', 'style', 'disabled', 'aria-busy', 'aria-pressed', 'title'],
  });
  window.addEventListener('scroll', syncVisibility, { passive: true });
  window.addEventListener('resize', syncVisibility);
  rebuild();
}

export function initLayout() {
  initSidebarCollapse();
  syncCompactHeader();
  window.addEventListener('scroll', syncCompactHeader, { passive: true });
  initMobileHeaderActions();
  layoutWasMobile = layoutWidth() > 0 && layoutWidth() < MOBILE_LAYOUT_MAX;
  applyMobileLayout();
  window.addEventListener('resize', function () {
    syncSidebarToWidth();
    if (mobileLayoutFrameScheduled) return;
    mobileLayoutFrameScheduled = true;
    requestAnimationFrame(function () {
      mobileLayoutFrameScheduled = false;
      applyMobileLayout();
    });
  });

  const mobileMenuBtn = document.getElementById('mobile-menu-btn');
  if (mobileMenuBtn) {
    mobileMenuBtn.addEventListener('click', function () {
      const close = document.querySelector('.modal-overlay:not(.confirm-overlay):not(.is-closing) .modal-close');
      if (close) {
        close.click();
        // несохраненные правки: диалог остался и спросил подтверждение, меню не открываем
        if (document.querySelector('.modal-overlay:not(.confirm-overlay):not(.is-closing)')) return;
      }
      const sidebar = document.getElementById('sidebar');
      if (sidebar && sidebar.classList.contains('open')) closeMobileMenu();
      else openMobileMenu();
    });
  }
  const sidebarBackdrop = document.getElementById('sidebar-backdrop');
  if (sidebarBackdrop) {
    sidebarBackdrop.addEventListener('click', function () {
      if (sidebarOverlayOpen) closeSidebarOverlay();
      else closeMobileMenu();
    });
  }

}
