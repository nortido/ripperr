import { hydrateIcons } from './icons.js';
import { syncNameFilterLayout } from './namefilter.js';

export const THEME_KEY = 'trackerr-theme';
export const SIDEBAR_COLLAPSED_KEY = 'trackerr-sidebar-collapsed';
export const PAGE_SCALE_BASE = 480;
// ограничен только сверху. Выше 1 масштаб раздувал вёрстку: на 880px выходило
// 1.83 и заголовок рисовался 44px вместо 24. Снизу ограничивать нельзя: мобильный
// макет свёрстан под 480px, и ровно w/480 это единственный масштаб, при котором
// он влезает в экран целиком. С порогом 0.8 на ширине меньше 384px приложение
// получало меньше 480 и правый край уезжал за экран
export const PAGE_SCALE_MAX = 1;
export const MOBILE_LAYOUT_MAX = 900;
export let layoutWasMobile = false;

export const ADVANCED_KEY = 'trackerr-advanced';

// признак висит на корне документа, а не в состоянии карточек: расширенные
// поля прячет CSS, поэтому переключатель не требует перерисовки трекеров и не
// мешает несохранённым правкам в скрытых полях
export function applyAdvanced(on) {
  if (on) document.documentElement.setAttribute('data-advanced', '1');
  else document.documentElement.removeAttribute('data-advanced');
  try {
    localStorage.setItem(ADVANCED_KEY, on ? '1' : '0');
  } catch (_) { /* ponytail: localStorage может быть недоступен */ }
}

export function advancedEnabled() {
  return document.documentElement.getAttribute('data-advanced') === '1';
}

export function initAdvanced() {
  let on = false;
  try {
    on = localStorage.getItem(ADVANCED_KEY) === '1';
  } catch (_) { /* ponytail: localStorage может быть недоступен */ }
  if (on) document.documentElement.setAttribute('data-advanced', '1');
}

export function applyTheme(theme) {
  const t = theme === 'light' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', t);
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch (_) { /* ponytail: localStorage может быть недоступен */ }
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
  } catch (_) { /* ponytail: localStorage может быть недоступен */ }
}

// ширина макетного вьюпорта, а не window.innerWidth: на iOS тот меняется при
// пинч-зуме и шлёт resize, из-за чего приложение пересчитывало масштаб прямо под
// пальцами и уезжало с экрана; clientWidth зум не трогает
export function layoutWidth() {
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
  // подсказка нужна только когда подпись скрыта. title="" это не отсутствие
  // атрибута, а пустой атрибут, который остаётся висеть в разметке
  if (brand) {
    const showFull = !collapsed || overlay;
    brand.textContent = showFull ? 'trackerr' : 't';
    if (showFull) brand.removeAttribute('title');
    else brand.setAttribute('title', 'trackerr');
  }
  const hideLabels = collapsed && !overlay;
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

export function initSidebarCollapse() {
  const btn = document.getElementById('sidebar-collapse-btn');
  if (btn) {
    btn.addEventListener('click', function () {
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
  const w = layoutWidth();
  const mobile = w > 0 && w < MOBILE_LAYOUT_MAX;
  const layoutChanged = mobile !== layoutWasMobile;
  if (mobile) {
    const scale = w / PAGE_SCALE_BASE;
    document.documentElement.classList.add('mobile-layout');
    // масштабируем только когда экран уже макета. Раньше класс висел всегда и на
    // 880px давал zoom 1.83: заголовок рисовался 44px вместо 24, в окно влезала
    // одна строка таблицы. Начиная с 480px ужимать нечего
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

// свёрнутое состояние выводится из ширины окна, поэтому должно попасть в тот же
// кадр, что и событие resize: перенос в rAF сначала рисует боковую панель развёрнутой
// и только потом сворачивает её. Выполняется до придушенного applyMobileLayout.
export function syncSidebarToWidth() {
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

export function initLayout() {
  initSidebarCollapse();
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
