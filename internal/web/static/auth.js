export let authState = null;
export let authRedirecting = false;
export const AUTH_SETUP_SKIP_KEY = 'trackerr.auth.setupSkipped';

export function gotoLogin() {
  if (authRedirecting) return;
  authRedirecting = true;
  const next = location.pathname + location.search + location.hash;
  location.href = '/login?next=' + encodeURIComponent(next);
}

export async function loadAuthState() {
  try {
    const res = await fetch('/api/auth/state');
    if (!res.ok) return;
    authState = await res.json();
  } catch (_) {
    return;
  }
  if (!authState.configured && !authSetupSkipped()) { gotoLogin(); return; }
  const logoutBtn = document.getElementById('logout-btn');
  // under LAN bypass logout is a no-op: the cookie is cleared but the request is
  // let in by IP anyway, so the button is hidden rather than left dead
  if (logoutBtn) logoutBtn.classList.toggle('hidden', !(authState && authState.configured && !authState.lan_bypass));
}

export function authSetupSkipped() {
  try {
    return localStorage.getItem(AUTH_SETUP_SKIP_KEY) === '1';
  } catch (_) {
    return false;
  }
}

export function bindLogout() {
  const btn = document.getElementById('logout-btn');
  if (!btn) return;
  btn.addEventListener('click', async function () {
    try {
      await fetch('/api/auth/logout', { method: 'POST' });
    } catch (_) { /* ponytail: best-effort */ }
    try {
      localStorage.setItem(AUTH_SETUP_SKIP_KEY, '1');
    } catch (_) { /* ponytail: best-effort */ }
    location.reload();
  });
}


