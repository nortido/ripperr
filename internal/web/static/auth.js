export let authState = null;
export let authRedirecting = false;

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
  if (!authState.configured) { gotoLogin(); return; }
  const logoutBtn = document.getElementById('logout-btn');
  if (logoutBtn) logoutBtn.classList.toggle('hidden', !authState.configured);
  showVersion(authState.version);
}

function showVersion(version) {
  const el = document.getElementById('app-version');
  if (!el || !version) return;
  el.textContent = /^\d/.test(version) ? 'v' + version : version;
  el.hidden = false;
  // в свернутой панели текст скрыт, версия остается в подсказке
  el.parentElement.title = 'Ripperr ' + el.textContent;
}

export function bindLogout() {
  const btn = document.getElementById('logout-btn');
  if (!btn) return;
  btn.addEventListener('click', async function () {
    try {
      await fetch('/api/auth/logout', { method: 'POST', headers: { 'X-Ripperr': '1' } });
    } catch (_) { /* не дошло, ну и ладно */ }
    location.reload();
  });
}


