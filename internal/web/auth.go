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
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
	"trackerr/internal/config"
	"trackerr/internal/storage/client/sqlite"
)

const (
	authCookieName  = "trackerr_session"
	authSessionTTL  = 30 * 24 * time.Hour
	pbkdf2Iter      = 600000
	pbkdf2KeyLen    = 32
	authSaltLen     = 32
	authSecretLen   = 32
	minPasswordLen  = 8
	maxLoginFails   = 5
	loginLockWindow = time.Minute
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

func (s *Server) loadAuth(ctx context.Context) (*authDoc, error) {
	// loaded истинно даже когда doc нулевой: "авторизация не настроена" это тоже
	// закешированное состояние, а не промах кеша.
	if doc, loaded := s.authCache.get(); loaded {
		return doc, nil
	}

	raw, ok, err := s.db.GetSetting(ctx, sqlite.SettingAuth)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.authCache.set(nil)
		return nil, nil
	}
	var doc authDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	s.authCache.set(&doc)
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

func verifySession(secret []byte, username, value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	expStr, usernameB64, hmacB64 := parts[0], parts[1], parts[2]

	usernameBytes, err := base64.RawURLEncoding.DecodeString(usernameB64)
	if err != nil {
		return false
	}
	if username != string(usernameBytes) {
		return false
	}

	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() >= expUnix {
		return false
	}

	expectedSig := hmac.New(sha256.New, secret)
	expectedSig.Write([]byte(expStr + "." + usernameB64))
	expectedHmacB64 := base64.RawURLEncoding.EncodeToString(expectedSig.Sum(nil))

	return hmac.Equal([]byte(hmacB64), []byte(expectedHmacB64))
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, doc *authDoc) error {
	secret, err := base64.RawURLEncoding.DecodeString(doc.Secret)
	if err != nil {
		return err
	}
	exp := time.Now().Add(authSessionTTL)
	value := signSession(secret, doc.Username, exp)

	secure := r.TLS != nil
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
	if s.webConfig().TrustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				if ip := net.ParseIP(strings.TrimSpace(parts[0])); ip != nil {
					return ip
				}
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func isLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

type loginThrottle struct {
	mu    sync.Mutex
	fails map[string]*loginFailure
}

type loginFailure struct {
	count int
	until time.Time
}

var loginFails = &loginThrottle{fails: map[string]*loginFailure{}}

func (t *loginThrottle) locked(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.fails[key]
	if !ok {
		return false
	}
	if time.Now().Before(rec.until) {
		return true
	}
	delete(t.fails, key)
	return false
}

func (t *loginThrottle) fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.fails[key]
	if !ok {
		rec = &loginFailure{}
		t.fails[key] = rec
	}
	rec.count++
	if rec.count >= maxLoginFails {
		rec.until = time.Now().Add(loginLockWindow)
		rec.count = 0
	}
}

func (t *loginThrottle) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}

func (s *Server) authorized(r *http.Request, doc *authDoc) bool {
	if doc == nil {
		return true
	}

	if s.webConfig().LanBypass && isLocalIP(s.clientIP(r)) {
		return true
	}

	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}

	secret, err := base64.RawURLEncoding.DecodeString(doc.Secret)
	if err != nil {
		return false
	}

	return verifySession(secret, doc.Username, cookie.Value)
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		if !s.authorized(r, doc) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
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
	lanBypass := doc != nil && s.webConfig().LanBypass && isLocalIP(s.clientIP(r))

	username := ""
	if doc != nil {
		username = doc.Username
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    configured,
		"authenticated": authenticated,
		"lan_bypass":    lanBypass,
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

	doc, err := s.loadAuth(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}
	if doc != nil {
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

	if loginFails.locked(ipStr) {
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
		loginFails.fail(ipStr)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	loginFails.reset(ipStr)

	if err := s.setSessionCookie(w, r, doc); err != nil {
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
