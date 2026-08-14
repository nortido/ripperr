import { syncNameFilterLayout } from './components.js';
import { hydrateIcons } from './icons.js';

export const THEME_KEY = 'trackerr-theme';
export const SIDEBAR_COLLAPSED_KEY = 'trackerr-sidebar-collapsed';
export const PAGE_SCALE_BASE = 480;
export const MOBILE_LAYOUT_MAX = 900;
export let layoutWasMobile = false;

export function applyTheme(theme) {
  const t = theme === 'light' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', t);
  document.querySelectorAll('.theme-toggle').forEach(function (btn) {
    btn.setAttribute('aria-pressed', t === 'dark' ? 'true' : 'false');
  });
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

export function initTheme() {
  const theme = document.documentElement.getAttribute('data-theme');
  document.querySelectorAll('.theme-toggle').forEach(function (btn) {
    btn.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
  });
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
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

export const SIDEBAR_WIDE_MIN = 1200;
export let sidebarOverlayOpen = false;

export function isSidebarOverlayWidth() {
  const w = window.innerWidth;
  return w >= 901 && w < SIDEBAR_WIDE_MIN;
}

export function effectiveSidebarCollapsed() {
  if (window.innerWidth <= 900) return false;
  if (window.innerWidth < SIDEBAR_WIDE_MIN) return true;
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
  const brand = document.getElementById('sidebar-brand');
  const overlay = isSidebarOverlayWidth() && sidebarOverlayOpen;
  if (btn) {
    const icon = btn.querySelector('[data-icon]');
    if (icon) {
      if (isSidebarOverlayWidth()) {
        icon.setAttribute('data-icon', overlay ? 'chevronLeft' : 'chevronRight');
      } else {
        icon.setAttribute('data-icon', collapsed ? 'chevronRight' : 'chevronLeft');
      }
      hydrateIcons(btn);
    }
  }
  if (brand) {
    const showFull = !collapsed || overlay;
    brand.textContent = showFull ? 'trackerr' : 't';
    brand.setAttribute('title', showFull ? '' : 'trackerr');
  }
  const hideLabels = collapsed && !overlay;
  document.querySelectorAll('.sidebar-nav .nav-link, .sidebar-footer .nav-link').forEach(function (link) {
    const label = link.querySelector('.nav-label');
    if (label) link.setAttribute('title', hideLabels ? label.textContent : '');
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

export function initSidebarCollapse() {
  const btn = document.getElementById('sidebar-collapse-btn');
  if (btn) {
    btn.addEventListener('click', function () {
      const w = window.innerWidth;
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
    });
  }
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
  const w = window.innerWidth;
  const mobile = w > 0 && w < MOBILE_LAYOUT_MAX;
  const layoutChanged = mobile !== layoutWasMobile;
  if (mobile) {
    const scale = w / PAGE_SCALE_BASE;
    document.documentElement.classList.add('mobile-layout');
    app.classList.add('page-scaled');
    // on the root so the value survives .app being re-rendered
    document.documentElement.style.setProperty('--page-scale', String(scale));
  } else {
    document.documentElement.classList.remove('mobile-layout');
    app.classList.remove('page-scaled');
    document.documentElement.style.removeProperty('--page-scale');
  }
  layoutWasMobile = mobile;
  refreshSidebarLayout();
  if (layoutChanged) syncNameFilterLayout();
}

// the collapsed state is derived from the window width, so it has to land in the
// same frame as the resize event: deferring it to rAF paints the sidebar expanded
// first and then collapses it. Runs before the throttled applyMobileLayout.
export function syncSidebarToWidth() {
  const mobile = window.innerWidth > 0 && window.innerWidth < MOBILE_LAYOUT_MAX;
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

export function initLayout() {
  initSidebarCollapse();
  layoutWasMobile = window.innerWidth > 0 && window.innerWidth < MOBILE_LAYOUT_MAX;
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

  document.querySelectorAll('.theme-toggle').forEach(function (btn) {
    btn.addEventListener('click', function () {
      const current = document.documentElement.getAttribute('data-theme');
      applyTheme(current === 'dark' ? 'light' : 'dark');
    });
  });
  const mobileMenuBtn = document.getElementById('mobile-menu-btn');
  if (mobileMenuBtn) {
    mobileMenuBtn.addEventListener('click', function () {
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
