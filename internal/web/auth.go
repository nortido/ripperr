package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
	"ripperr/internal/config"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
)

const (
	authCookieName  = "ripperr_session"
	authSessionTTL  = 30 * 24 * time.Hour
	authRenewAfter  = 24 * time.Hour
	pbkdf2Iter      = 600000
	pbkdf2KeyLen    = 32
	authSaltLen     = 32
	authSecretLen   = 32
	minPasswordLen  = 8
	maxLoginFails   = 5
	loginLockWindow = 24 * time.Hour
	pbkdf2Slots     = 2
	maxThrottleKeys = 1024
)

const (
	deviceCookieName = "ripperr_device"
	deviceTTL        = 400 * 24 * time.Hour
	deviceKeyLen     = 32
	deviceIDLen      = 16
)

const hostCookiePrefix = "__Host-"

const maxCookieCandidates = 16

type authDoc struct {
	Username  string `json:"username"`
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
	Secret    string `json:"secret"`
	CreatedAt string `json:"created_at"`
	DeviceKey string `json:"device_key,omitempty"`
}

type authCache struct {
	mu     sync.RWMutex
	loaded bool
	doc    *authDoc
}

func (c *authCache) get() (*authDoc, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.doc, c.loaded
}

func (c *authCache) set(doc *authDoc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.doc = doc
	c.loaded = true
}

func (c *authCache) fill(doc *authDoc) *authDoc {
	c.mu.Lock()
	defer c.mu.Unlock()
	// чтение, начатое до setup, не должно затереть сохраненный аккаунт устаревшим nil
	if c.loaded {
		return c.doc
	}
	c.doc = doc
	c.loaded = true
	return doc
}

func (s *Server) loadAuth(ctx context.Context) (*authDoc, error) {
	// loaded true и при нулевом doc: "авторизация не настроена" тоже лежит в кеше
	if doc, loaded := s.authCache.get(); loaded {
		return doc, nil
	}
	doc, err := s.readAuth(ctx)
	if err != nil {
		return nil, err
	}
	return s.authCache.fill(doc), nil
}

func (s *Server) readAuth(ctx context.Context) (*authDoc, error) {
	raw, ok, err := s.db.GetSetting(ctx, sqlite.SettingAuth)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var doc authDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (s *Server) saveAuth(ctx context.Context, doc *authDoc) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := s.db.SaveSetting(ctx, sqlite.SettingAuth, raw); err != nil {
		return err
	}
	s.authCache.set(doc)
	return nil
}

func hashPassword(password string, salt []byte) []byte {
	return pbkdf2.Key([]byte(password), salt, pbkdf2Iter, pbkdf2KeyLen, sha256.New)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func passwordMatches(doc *authDoc, password string) (bool, error) {
	salt, err := base64.RawURLEncoding.DecodeString(doc.Salt)
	if err != nil {
		return false, err
	}
	storedHash, err := base64.RawURLEncoding.DecodeString(doc.Hash)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(hashPassword(password, salt), storedHash) == 1, nil
}

func signSession(secret []byte, username string, exp time.Time) string {
	expStr := strconv.FormatInt(exp.Unix(), 10)
	usernameB64 := base64.RawURLEncoding.EncodeToString([]byte(username))
	msg := expStr + "." + usernameB64
	sig := hmac.New(sha256.New, secret)
	sig.Write([]byte(msg))
	hmacB64 := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
	return msg + "." + hmacB64
}

func sessionExpiry(secret []byte, username, value string) (time.Time, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	expStr, usernameB64, hmacB64 := parts[0], parts[1], parts[2]

	usernameBytes, err := base64.RawURLEncoding.DecodeString(usernameB64)
	if err != nil {
		return time.Time{}, false
	}
	if username != string(usernameBytes) {
		return time.Time{}, false
	}

	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	if time.Now().Unix() >= expUnix {
		return time.Time{}, false
	}

	expectedSig := hmac.New(sha256.New, secret)
	expectedSig.Write([]byte(expStr + "." + usernameB64))
	expectedHmacB64 := base64.RawURLEncoding.EncodeToString(expectedSig.Sum(nil))

	if !hmac.Equal([]byte(hmacB64), []byte(expectedHmacB64)) {
		return time.Time{}, false
	}
	return time.Unix(expUnix, 0), true
}

func verifyDevice(key []byte, value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "", false
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	if _, ok := sessionExpiry(key, string(id), value); !ok {
		return "", false
	}
	return string(id), true
}

func newDeviceID() (string, error) {
	b, err := randomBytes(deviceIDLen)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func epochDeviceID(key []byte, secret string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("secret:" + secret))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:deviceIDLen])
}

func requestScheme(r *http.Request) (secure, known bool) {
	if r.TLS != nil {
		return true, true
	}
	// за прокси keenetic TLS снимается до нас, схему видно только по Origin или Referer браузера
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" && behindStrippingProxy(r.Host) {
		return true, true
	}
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false, false
	}
	return u.Scheme == "https", true
}

func behindStrippingProxy(host string) bool {
	// https-прокси keenetic срезает Origin и Referer и ставит Host без порта, а напрямую по IP заходят с портом
	return net.ParseIP(host) != nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, doc *authDoc) error {
	secret, err := base64.RawURLEncoding.DecodeString(doc.Secret)
	if err != nil {
		return err
	}
	exp := time.Now().Add(authSessionTTL)
	value := signSession(secret, doc.Username, exp)

	secure, _ := requestScheme(r)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(authCookieName, secure),
		Value:    value,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(authSessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	if secure {
		expireCookie(w, authCookieName, true)
	}
	return nil
}

func (s *Server) setDeviceCookie(w http.ResponseWriter, r *http.Request, key []byte, id string) {
	exp := time.Now().Add(deviceTTL)
	secure, _ := requestScheme(r)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(deviceCookieName, secure),
		Value:    signSession(key, id, exp),
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(deviceTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	if secure {
		expireCookie(w, deviceCookieName, true)
	}
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	expireCookie(w, authCookieName, false)
	// без Secure браузер не даст стереть куку __Host-
	if secure, _ := requestScheme(r); secure {
		expireCookie(w, hostCookiePrefix+authCookieName, true)
	}
}

func cookieName(base string, secure bool) string {
	if secure {
		return hostCookiePrefix + base
	}
	return base
}

type cookieCandidate struct {
	value  string
	legacy bool
}

func cookieCandidates(r *http.Request, base string) []cookieCandidate {
	// __Host- браузер принимает только от этого хоста по https и без Domain, соседний поддомен
	// ее не подложит. Пробуем ее первой, потом все старые: чужая кука не заслонит верную
	var prefixed, legacy []cookieCandidate
	for _, c := range r.Cookies() {
		switch c.Name {
		case hostCookiePrefix + base:
			prefixed = append(prefixed, cookieCandidate{value: c.Value})
		case base:
			legacy = append(legacy, cookieCandidate{value: c.Value, legacy: true})
		}
	}
	out := append(prefixed, legacy...)
	// каждый кандидат стоит HMAC: пачка поддельных кук одного имени не должна множить работу
	return out[:min(len(out), maxCookieCandidates)]
}

func expireCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func (s *Server) webConfig() config.Web {
	return s.effective().Web
}

func (s *Server) clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote := net.ParseIP(host)
	if !s.webConfig().TrustedProxy || !isLocalIP(remote) {
		return remote
	}

	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return remote
	}
	// левые записи пишет клиент, верим только последней: ее дописал наш прокси
	last := values[len(values)-1]
	if i := strings.LastIndexByte(last, ','); i >= 0 {
		last = last[i+1:]
	}
	return net.ParseIP(strings.TrimSpace(last))
}

func isLocalHost(hostport string, allowed []string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = normalizeHost(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".lan", ".home.arpa", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	for _, entry := range allowed {
		if normalizeHost(entry) == host {
			return true
		}
	}
	return false
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func isLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

type loginThrottle struct {
	mu    sync.Mutex
	now   func() time.Time
	fails map[string]*loginFailure
}

type loginFailure struct {
	first time.Time
	count int
	until time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{now: time.Now, fails: map[string]*loginFailure{}}
}

func (t *loginThrottle) reserve(key string) bool {
	ok, room := t.reserveIfRoom(key)
	return ok && room
}

func (t *loginThrottle) reserveIfRoom(key string) (ok, room bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	rec, found := t.fails[key]
	if found && now.Before(rec.until) {
		return false, true
	}
	if !found || !rec.until.IsZero() || !now.Before(rec.first.Add(loginLockWindow)) {
		if !found && len(t.fails) >= maxThrottleKeys {
			t.prune(now)
			// отказ вместо вытеснения: иначе атакующий со многими адресами
			// вытеснял бы свои же записи и получал новые попытки
			if len(t.fails) >= maxThrottleKeys {
				return false, false
			}
		}
		rec = &loginFailure{first: now}
		t.fails[key] = rec
	}
	rec.count++
	if rec.count >= maxLoginFails {
		rec.until = now.Add(loginLockWindow)
	}
	return true, true
}

func (t *loginThrottle) locked(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.fails[key]
	return ok && t.now().Before(rec.until)
}

func (t *loginThrottle) prune(now time.Time) {
	for key, rec := range t.fails {
		if !now.Before(rec.first.Add(loginLockWindow)) && !now.Before(rec.until) {
			delete(t.fails, key)
		}
	}
}

func (t *loginThrottle) status(key string) (left int, wait time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	rec, ok := t.fails[key]
	switch {
	case !ok && len(t.fails) >= maxThrottleKeys:
		return 0, loginLockWindow
	case !ok:
		return maxLoginFails, 0
	case now.Before(rec.until):
		return 0, rec.until.Sub(now)
	case !rec.until.IsZero() || !now.Before(rec.first.Add(loginLockWindow)):
		return maxLoginFails, 0
	}
	return max(maxLoginFails-rec.count, 0), 0
}

func waitSeconds(wait time.Duration) int {
	if wait <= 0 {
		return 0
	}
	return int((wait + time.Second - 1) / time.Second)
}

func writeLoginLocked(w http.ResponseWriter, wait time.Duration) {
	secs := max(waitSeconds(wait), 1)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, loginErrorResp{Error: "too many attempts, try later", RetryAfter: secs})
}

func (t *loginThrottle) success(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}

func (s *Server) acquireHashSlot() bool {
	select {
	case s.hashSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseHashSlot() {
	<-s.hashSlots
}

func (s *Server) authorized(r *http.Request, doc *authDoc) bool {
	_, _, ok := s.session(r, doc)
	return ok
}

func (s *Server) session(r *http.Request, doc *authDoc) (exp time.Time, legacy, ok bool) {
	if doc == nil {
		return time.Time{}, false, false
	}

	secret, err := base64.RawURLEncoding.DecodeString(doc.Secret)
	if err != nil {
		return time.Time{}, false, false
	}

	for _, c := range cookieCandidates(r, authCookieName) {
		if exp, ok := sessionExpiry(secret, doc.Username, c.value); ok {
			return exp, c.legacy, true
		}
	}
	return time.Time{}, false, false
}

func (s *Server) trustedDevice(r *http.Request, doc *authDoc) (id string, legacy, ok bool) {
	if doc == nil || doc.DeviceKey == "" {
		return "", false, false
	}
	key, err := base64.RawURLEncoding.DecodeString(doc.DeviceKey)
	if err != nil {
		return "", false, false
	}
	for _, c := range cookieCandidates(r, deviceCookieName) {
		if id, ok := verifyDevice(key, c.value); ok {
			return id, c.legacy, true
		}
	}
	return "", false, false
}

func (s *Server) deviceKey(ctx context.Context, doc *authDoc) ([]byte, error) {
	if doc != nil && doc.DeviceKey != "" {
		return base64.RawURLEncoding.DecodeString(doc.DeviceKey)
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()

	cur, err := s.readAuth(ctx)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, errors.New("auth not configured")
	}
	if cur.DeviceKey != "" {
		return base64.RawURLEncoding.DecodeString(cur.DeviceKey)
	}
	// ключ не выводим из Secret: выход меняет Secret, а доверие устройств должно его пережить
	key, err := randomBytes(deviceKeyLen)
	if err != nil {
		return nil, err
	}
	nd := *cur
	nd.DeviceKey = base64.RawURLEncoding.EncodeToString(key)
	if err := s.saveAuth(ctx, &nd); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Server) issueDevice(ctx context.Context, w http.ResponseWriter, r *http.Request, doc *authDoc) {
	key, err := s.deviceKey(ctx, doc)
	if err != nil {
		return
	}
	id, err := newDeviceID()
	if err != nil {
		return
	}
	s.setDeviceCookie(w, r, key, id)
}

func (s *Server) reserveAttempt(r *http.Request, doc *authDoc, ipStr string) (th *loginThrottle, key, kind string, ok bool) {
	// запрос идет ровно в один счет: блокировка адреса не задевает доверенные устройства,
	// а их успех не сбрасывает счет адреса
	if id, _, trusted := s.trustedDevice(r, doc); trusted {
		// нет места в таблице устройств: считаем по адресу, отказ запер бы доверенный браузер с верным паролем
		if ok, room := s.deviceFails.reserveIfRoom(id); room {
			return s.deviceFails, id, "device", ok
		}
	}
	return s.loginFails, ipStr, "address", s.loginFails.reserve(ipStr)
}

func (s *Server) lockedKey(r *http.Request, doc *authDoc, ipStr string) (th *loginThrottle, key string, locked bool) {
	if id, _, trusted := s.trustedDevice(r, doc); trusted {
		// без записи устройства reserveAttempt может уйти в счет адреса, а может и нет:
		// здесь не угадываем и отказ оставляем ему
		return s.deviceFails, id, s.deviceFails.locked(id)
	}
	return s.loginFails, ipStr, s.loginFails.locked(ipStr)
}

func logLock(kind, ipStr string) {
	logger.Warn("auth: sign-in locked after failed attempts (" + kind + "), client " + ipStr)
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func sameOrigin(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	// прокси keenetic подменяет Host на адрес бэкенда, так что имена из WEB_ALLOWED_HOSTS сверяем с самим Origin
	host := normalizeHost(u.Hostname())
	if host == "" {
		return false
	}
	for _, entry := range allowed {
		if normalizeHost(entry) == host {
			return true
		}
	}
	return false
}

func csrfProof(r *http.Request) bool {
	if r.Header.Get("X-Ripperr") != "1" {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin" || site == "none"
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// прокси срезает Origin, а свой заголовок с чужого сайта требует preflight,
		// который сервер никогда не одобряет, поэтому без него запрос не отправить
		if strings.HasPrefix(r.URL.Path, "/api/") && !safeMethod(r.Method) && (!sameOrigin(r, s.webConfig().AllowedHosts) || !csrfProof(r)) {
			writeError(w, http.StatusForbidden, "cross-origin request")
			return
		}

		if !strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		doc, err := s.loadAuth(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "auth error")
			return
		}
		if doc == nil {
			writeError(w, http.StatusUnauthorized, "account setup required")
			return
		}

		exp, legacy, ok := s.session(r, doc)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// без схемы продление могло бы заменить Secure куку обычной.
		// Старое имя по https переносим на __Host-, чтобы вход пережил смену имени
		secure, known := requestScheme(r)
		if known && (time.Until(exp) < authSessionTTL-authRenewAfter || legacy && secure) {
			if err := s.setSessionCookie(w, r, doc); err != nil {
				writeError(w, http.StatusInternalServerError, "auth error")
				return
			}
		}

		// id от секрета эпохи, а не от значения сессии: старые значения действуют до срока,
		// продление выдает новые, и держатель сессии набрал бы сколько угодно счетов
		if known {
			id, devLegacy, trusted := s.trustedDevice(r, doc)
			switch {
			case !trusted:
				if key, err := s.deviceKey(ctx, doc); err == nil {
					s.setDeviceCookie(w, r, key, epochDeviceID(key, doc.Secret))
				}
			case devLegacy && secure:
				// тот же id: счет попыток и доверие устройства переезжают вместе с кукой
				if key, err := s.deviceKey(ctx, doc); err == nil {
					s.setDeviceCookie(w, r, key, id)
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	doc, err := s.loadAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	configured := doc != nil
	authenticated := s.authorized(r, doc)

	username := ""
	if authenticated {
		username = doc.Username
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    configured,
		"authenticated": authenticated,
		"username":      username,
	})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// без аккаунта сюда может постучаться страница с чужого домена через DNS rebinding,
	// она шлет свой домен в Host. Голый IP в Host ставит только https-прокси keenetic,
	// клиент из интернета Host не выбирает, так что первый аккаунт создают только из сети
	if behindStrippingProxy(r.Host) || !isLocalHost(r.Host, s.webConfig().AllowedHosts) {
		writeError(w, http.StatusForbidden, "account setup is allowed only from a local address")
		return
	}

	// аккаунт в кеше уже не пропадет: отвечаем без слота и базы. Кешу с nil не верим, это ниже
	if doc, loaded := s.authCache.get(); loaded && doc != nil {
		writeError(w, http.StatusConflict, "auth already configured")
		return
	}

	// слот до authMu: так за замком ждут не больше pbkdf2Slots запросов
	if !s.acquireHashSlot() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts at once, try later")
		return
	}
	defer s.releaseHashSlot()

	// проверка "аккаунта нет" и запись под одним замком, иначе параллельные setup создадут несколько
	s.authMu.Lock()
	defer s.authMu.Unlock()

	// читаем базу мимо кеша: в кеше может лежать устаревший nil
	doc, err := s.readAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	if doc != nil {
		s.authCache.set(doc)
		writeError(w, http.StatusConflict, "auth already configured")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 64 {
		writeError(w, http.StatusBadRequest, "username must be 3..64 chars")
		return
	}

	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be at least 8 chars")
		return
	}

	salt, err := randomBytes(authSaltLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	secret, err := randomBytes(authSecretLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	// свой ключ, а не s.deviceKey: тот берет authMu, который здесь уже занят
	devKey, err := randomBytes(deviceKeyLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	devID, err := newDeviceID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	hash := hashPassword(req.Password, salt)

	doc = &authDoc{
		Username:  req.Username,
		Salt:      base64.RawURLEncoding.EncodeToString(salt),
		Hash:      base64.RawURLEncoding.EncodeToString(hash),
		Secret:    base64.RawURLEncoding.EncodeToString(secret),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		DeviceKey: base64.RawURLEncoding.EncodeToString(devKey),
	}

	if err := s.saveAuth(ctx, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	if err := s.setSessionCookie(w, r, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	s.setDeviceCookie(w, r, devKey, devID)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	doc, err := s.loadAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	if doc == nil {
		writeError(w, http.StatusConflict, "auth not configured")
		return
	}

	ipStr := "unknown"
	if ip := s.clientIP(r); ip != nil {
		ipStr = ip.String()
	}

	// запертый ключ reserveAttempt отклонит и так, а за общим адресом keenetic
	// такие запросы иначе занимали бы слоты всех остальных
	if th, key, locked := s.lockedKey(r, doc, ipStr); locked {
		_, wait := th.status(key)
		writeLoginLocked(w, wait)
		return
	}

	// отказ по слоту не проверял пароль, поэтому слот берем до reserve
	if !s.acquireHashSlot() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts at once, try later")
		return
	}
	defer s.releaseHashSlot()

	// попытку учитываем до хеширования, иначе параллельные запросы проскочат лимит
	th, key, kind, ok := s.reserveAttempt(r, doc, ipStr)
	if !ok {
		_, wait := th.status(key)
		writeLoginLocked(w, wait)
		return
	}

	passwordMatch, err := passwordMatches(doc, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	usernameMatch := strings.EqualFold(req.Username, doc.Username)

	if !usernameMatch || !passwordMatch {
		left, wait := th.status(key)
		if wait > 0 {
			logLock(kind, ipStr)
		}
		writeJSON(w, http.StatusUnauthorized, loginErrorResp{Error: "invalid credentials", AttemptsLeft: &left, RetryAfter: waitSeconds(wait)})
		return
	}

	th.success(key)

	if err := s.setSessionCookie(w, r, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	s.issueDevice(ctx, w, r, doc)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	doc, err := s.loadAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	// без живой сессии только чистим куку, иначе разлогинить владельца сможет кто угодно
	if s.authorized(r, doc) {
		if err := s.rotateSecret(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "auth error")
			return
		}
	}

	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) rotateSecret(ctx context.Context) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	doc, err := s.readAuth(ctx)
	if err != nil || doc == nil {
		return err
	}
	secret, err := randomBytes(authSecretLen)
	if err != nil {
		return err
	}
	nd := *doc
	nd.Secret = base64.RawURLEncoding.EncodeToString(secret)
	return s.saveAuth(ctx, &nd)
}

func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	doc, err := s.loadAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	if doc == nil {
		writeError(w, http.StatusUnauthorized, "account setup required")
		return
	}
	// чужие и битые запросы отсекаем до слота и лимитера, попытку тратит только реальная проверка пароля
	if !s.authorized(r, doc) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.New) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be at least 8 chars")
		return
	}

	if !s.acquireHashSlot() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts at once, try later")
		return
	}
	defer s.releaseHashSlot()

	s.authMu.Lock()
	defer s.authMu.Unlock()

	// сессию сверяем еще раз по свежему документу: logout или другая смена могли сменить секрет, пока ждали замок
	cur, err := s.readAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	if !s.authorized(r, cur) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	ipStr := "unknown"
	if ip := s.clientIP(r); ip != nil {
		ipStr = ip.String()
	}

	// общий счет с входом: иначе украденная кука дала бы лишние попытки подбора
	th, key, kind, allowed := s.reserveAttempt(r, cur, ipStr)
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try later")
		return
	}

	ok, err := passwordMatches(cur, req.Current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	// 403, а не 401: сессия жива, неверен только пароль, и интерфейс не уводит на вход
	if !ok {
		if _, wait := th.status(key); wait > 0 {
			logLock(kind, ipStr)
		}
		writeError(w, http.StatusForbidden, "invalid current password")
		return
	}
	th.success(key)

	salt, err := randomBytes(authSaltLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	secret, err := randomBytes(authSecretLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	// новый ключ устройств: знавший пароль мог наделать сколько угодно кук устройств, отозвать их можно только так.
	// Свой ключ, а не s.deviceKey: authMu здесь уже занят
	devKey, err := randomBytes(deviceKeyLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	devID, err := newDeviceID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	// новый секрет в той же записи, что и хеш: все старые куки перестают сходиться одним сохранением
	nd := *cur
	nd.Salt = base64.RawURLEncoding.EncodeToString(salt)
	nd.Hash = base64.RawURLEncoding.EncodeToString(hashPassword(req.New, salt))
	nd.Secret = base64.RawURLEncoding.EncodeToString(secret)
	nd.DeviceKey = base64.RawURLEncoding.EncodeToString(devKey)
	if err := s.saveAuth(ctx, &nd); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	if err := s.setSessionCookie(w, r, &nd); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	s.setDeviceCookie(w, r, devKey, devID)

	logger.Info("auth: password changed, other sessions revoked, client " + ipStr)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
