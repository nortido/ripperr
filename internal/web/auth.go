package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
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
	loginLockWindow = time.Minute
	pbkdf2Slots     = 2
	maxThrottleKeys = 1024
)

type authDoc struct {
	Username  string `json:"username"`
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
	Secret    string `json:"secret"`
	CreatedAt string `json:"created_at"`
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

func requestScheme(r *http.Request) (secure, known bool) {
	if r.TLS != nil {
		return true, true
	}
	// за прокси keenetic TLS снимается до нас, схему видно только по Origin или Referer браузера
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false, false
	}
	return u.Scheme == "https", true
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
		Name:     authCookieName,
		Value:    value,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(authSessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
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
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	rec, ok := t.fails[key]
	if ok && now.Before(rec.until) {
		return false
	}
	if !ok || !rec.until.IsZero() || !now.Before(rec.first.Add(loginLockWindow)) {
		if !ok && len(t.fails) >= maxThrottleKeys {
			t.prune(now)
		}
		rec = &loginFailure{first: now}
		t.fails[key] = rec
	}
	rec.count++
	if rec.count >= maxLoginFails {
		rec.until = now.Add(loginLockWindow)
	}
	return true
}

func (t *loginThrottle) prune(now time.Time) {
	for key, rec := range t.fails {
		if !now.Before(rec.first.Add(loginLockWindow)) && !now.Before(rec.until) {
			delete(t.fails, key)
		}
	}
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
	_, ok := s.session(r, doc)
	return ok
}

func (s *Server) session(r *http.Request, doc *authDoc) (time.Time, bool) {
	if doc == nil {
		return time.Time{}, false
	}

	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return time.Time{}, false
	}

	secret, err := base64.RawURLEncoding.DecodeString(doc.Secret)
	if err != nil {
		return time.Time{}, false
	}

	return sessionExpiry(secret, doc.Username, cookie.Value)
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

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !safeMethod(r.Method) && !sameOrigin(r, s.webConfig().AllowedHosts) {
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

		exp, ok := s.session(r, doc)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// без схемы продление могло бы заменить Secure куку обычной
		if _, known := requestScheme(r); known && time.Until(exp) < authSessionTTL-authRenewAfter {
			if err := s.setSessionCookie(w, r, doc); err != nil {
				writeError(w, http.StatusInternalServerError, "auth error")
				return
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

	// слот до authMu: так за замком ждут не больше pbkdf2Slots запросов
	if !s.acquireHashSlot() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts at once, try later")
		return
	}
	defer s.releaseHashSlot()

	// без аккаунта сюда может постучаться страница с чужого домена через DNS rebinding,
	// она шлет свой домен в Host
	if !isLocalHost(r.Host, s.webConfig().AllowedHosts) {
		writeError(w, http.StatusForbidden, "account setup is allowed only from a local address")
		return
	}

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

	hash := hashPassword(req.Password, salt)

	doc = &authDoc{
		Username:  req.Username,
		Salt:      base64.RawURLEncoding.EncodeToString(salt),
		Hash:      base64.RawURLEncoding.EncodeToString(hash),
		Secret:    base64.RawURLEncoding.EncodeToString(secret),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.saveAuth(ctx, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	if err := s.setSessionCookie(w, r, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

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

	// отказ по слоту не проверял пароль, поэтому слот берем до reserve
	if !s.acquireHashSlot() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts at once, try later")
		return
	}
	defer s.releaseHashSlot()

	// попытку учитываем до хеширования, иначе параллельные запросы проскочат лимит
	if !s.loginFails.reserve(ipStr) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try later")
		return
	}

	salt, err := base64.RawURLEncoding.DecodeString(doc.Salt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	storedHash, err := base64.RawURLEncoding.DecodeString(doc.Hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	computedHash := hashPassword(req.Password, salt)

	usernameMatch := strings.EqualFold(req.Username, doc.Username)
	passwordMatch := subtle.ConstantTimeCompare(computedHash, storedHash) == 1

	if !usernameMatch || !passwordMatch {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	s.loginFails.success(ipStr)

	if err := s.setSessionCookie(w, r, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

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

	clearSessionCookie(w)
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
