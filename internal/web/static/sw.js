self.addEventListener('install', function () {
  self.skipWaiting();
});

self.addEventListener('activate', function (event) {
  // без claim вкладки, открытые до регистрации, остаются чужими: navigate на них падает
  // и клик открывал второе окно. fetch не перехватываем, так что брать их безопасно
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', function (event) {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (_) { /* битый payload все равно показываем */ }
  // каждый push обязан показать уведомление, иначе iOS отзывает разрешение
  event.waitUntil(self.registration.showNotification(data.title || 'Ripperr', {
    body: data.body,
    icon: '/icon-192.png',
    data: { url: data.url || '/' },
  }));
});

self.addEventListener('notificationclick', function (event) {
  event.notification.close();
  const raw = new URL((event.notification.data && event.notification.data.url) || '/', self.location.origin);
  // чужой origin не открываем, только свой
  const target = (raw.origin === self.location.origin ? raw : new URL('/', self.location.origin)).href;
  event.waitUntil(clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (list) {
    const mine = list.filter(function (c) { return new URL(c.url).origin === self.location.origin; });
    const open = mine.find(function (c) { return c.focused; }) || mine.find(function (c) { return c.visibilityState === 'visible'; }) || mine[0];
    if (!open) return clients.openWindow(target);
    // вкладка уже на экране: если перейти не вышло, второе окно хуже, чем старая страница
    return open.focus().then(function (c) {
      return c.navigate(target).catch(function () { return c; });
    }).catch(function () { return clients.openWindow(target); });
  }));
});
