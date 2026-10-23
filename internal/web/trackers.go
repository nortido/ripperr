package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"ripperr/internal/config"
	"ripperr/internal/helper/formcreds"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/settings"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/registry"
)

func trackerName(id int) string {
	return registry.Key(id)
}

func trackerKnown(key string) bool {
	_, ok := registry.ByKey(key)
	return ok
}

func trackerProxyType(cfg config.App, key string) int {
	if it := cfg.Tracker.ByKey(key); it != nil {
		return it.ProxyType
	}
	return 0
}

func effectiveTrackerProxyType(cfg config.App, key string, configPatch json.RawMessage) int {
	if len(configPatch) > 0 && string(configPatch) != "null" {
		if overlay, err := settings.TrackerPatchFromJSON(configPatch); err == nil && overlay.ProxyType != nil {
			return *overlay.ProxyType
		}
	}
	return trackerProxyType(cfg, key)
}

func trackerCredsView(key string, it config.TrackerItem) (tmpl, login, password string, supported bool) {
	def, ok := registry.ByKey(key)
	if !ok || def.Login == nil {
		return "", "", "", false
	}
	body := it.Credentials
	if body == "" {
		body = def.Login.Template
	}
	// значение в теле главнее отдельных полей: его же отправит вход после Expand
	tmpl, login, password, hasLogin, hasPassword := formcreds.Templatize(body, def.Login.UserField, def.Login.PassField)
	if !hasLogin {
		login = it.Login
	}
	if !hasPassword {
		password = it.Password
	}
	return tmpl, login, password, true
}

func applyTrackerLogin(cfg config.App, key string, patch json.RawMessage) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(patch, &body); err != nil {
		return nil, err
	}
	// JSON null значит "поле не прислали", без этой отсечки Unmarshal отдал бы
	// пустую строку и затер сохраненный пароль
	stripped := false
	for _, field := range []string{"credentials", "login", "password"} {
		if raw, ok := body[field]; ok && string(raw) == "null" {
			delete(body, field)
			stripped = true
		}
	}
	rawCreds, sentCreds := body["credentials"]
	rawLogin, sentLogin := body["login"]
	rawPassword, sentPassword := body["password"]

	def, ok := registry.ByKey(key)
	if !ok || def.Login == nil {
		if !stripped && !sentLogin && !sentPassword {
			return patch, nil
		}
		delete(body, "login")
		delete(body, "password")
		return json.Marshal(body)
	}
	if !sentCreds && !sentLogin && !sentPassword {
		if !stripped {
			return patch, nil
		}
		return json.Marshal(body)
	}

	var stored config.TrackerItem
	if it := cfg.Tracker.ByKey(key); it != nil {
		stored = *it
	}
	tmpl, login, password, _ := trackerCredsView(key, stored)
	if sentCreds {
		var sent string
		if err := json.Unmarshal(rawCreds, &sent); err != nil {
			return nil, err
		}
		// маска значит "строку не трогали", остается шаблон из сохраненного
		if sent != settings.SecretMask {
			if sent == "" {
				sent = def.Login.Template
			}
			var bodyLogin, bodyPassword string
			var hasLogin, hasPassword bool
			tmpl, bodyLogin, bodyPassword, hasLogin, hasPassword = formcreds.Templatize(sent, def.Login.UserField, def.Login.PassField)
			if hasLogin {
				login = bodyLogin
			}
			if hasPassword {
				password = bodyPassword
			}
		}
	}
	if sentLogin {
		if err := json.Unmarshal(rawLogin, &login); err != nil {
			return nil, err
		}
	}
	if sentPassword {
		var sent string
		if err := json.Unmarshal(rawPassword, &sent); err != nil {
			return nil, err
		}
		// маска из формы оставляет пароль из строки или сохраненный, какой бы
		// credentials ни пришел
		if sent != settings.SecretMask {
			password = sent
		}
	}

	// пишем все три поля: старое тело с настоящими значениями или тело из env
	// переезжает в оверлей шаблоном при первом же сохранении
	for field, value := range map[string]string{"credentials": tmpl, "login": login, "password": password} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body[field] = encoded
	}
	return json.Marshal(body)
}

func prepareTrackerPatch(cfg config.App, key string, patch json.RawMessage) (json.RawMessage, error) {
	patch, _, err := prepareTrackerPatchInfo(cfg, key, patch)
	return patch, err
}

type trackerPatchInfo struct {
	removed   []string
	confirmed []string
}

func prepareTrackerPatchInfo(cfg config.App, key string, patch json.RawMessage) (json.RawMessage, trackerPatchInfo, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(patch, &body); err != nil {
		return nil, trackerPatchInfo{}, err
	}
	// дальше патч читают структуры, а json сопоставляет ключи без учета регистра:
	// "Domain" или "Cookies" обошли бы проверки домена и маски ниже
	for k := range body {
		for _, name := range trackerJSONFields {
			if k != name && strings.EqualFold(k, name) {
				return nil, trackerPatchInfo{}, fmt.Errorf("unknown field %q, did you mean %q", k, name)
			}
		}
	}
	for _, field := range []string{"retired", "unconfirmed"} {
		if _, sent := body[field]; sent {
			return nil, trackerPatchInfo{}, fmt.Errorf("%s is read-only", field)
		}
	}
	changed := false
	var info trackerPatchInfo
	var shown map[string]bool
	var seen []string
	if raw, sent := body["mirrors_confirm"]; sent {
		var confirm []string
		if string(raw) != "null" && json.Unmarshal(raw, &confirm) != nil {
			return nil, trackerPatchInfo{}, errors.New("mirrors_confirm must be a list of hosts")
		}
		shown = make(map[string]bool, len(confirm))
		for _, h := range confirm {
			shown[hostKey(h)] = true
		}
		delete(body, "mirrors_confirm")
		changed = true
	}
	if raw, sent := body["mirrors_seen"]; sent {
		var err error
		if info.removed, err = keepUnseenMirrors(cfg, key, body, raw); err != nil {
			return nil, trackerPatchInfo{}, err
		}
		_ = json.Unmarshal(raw, &seen)
		delete(body, "mirrors_seen")
		changed = true
	}
	if raw, sent := body["domain"]; sent && string(raw) != "null" {
		var domain string
		if json.Unmarshal(raw, &domain) != nil {
			return nil, trackerPatchInfo{}, errDomainReadOnly
		}
		current := effectiveTrackerDomain(cfg, key)
		if hostKey(domain) == hostKey(current) {
			// пробелы по краям не сохраняем, иначе следующий PUT с чистым доменом
			// уже не совпадет с сохраненным
			if domain != current {
				body["domain"], _ = json.Marshal(current)
				changed = true
			}
		} else {
			if err := swapTrackerDomain(cfg, key, body, domain, current, seen); err != nil {
				return nil, trackerPatchInfo{}, err
			}
			changed = true
		}
	}
	if ok, confirmed := recomputeMirrorLists(cfg, key, body, shown); ok {
		info.confirmed = confirmed
		changed = true
	}
	def, ok := registry.ByKey(key)
	loginForm := ok && def.Login != nil
	for _, field := range []string{"credentials", "cookies", "password"} {
		var v string
		if raw, ok := body[field]; !ok || json.Unmarshal(raw, &v) != nil {
			continue
		}
		// маска из GET значит "не менял": без поля остается сохраненное значение
		if v == settings.SecretMask {
			// у трекера с формой входа маску разбирает applyTrackerLogin: ей нужно
			// знать, что поле прислали
			if loginForm && field != "cookies" {
				continue
			}
			changed = true
			delete(body, field)
		} else if strings.Contains(v, settings.SecretMask) {
			return nil, trackerPatchInfo{}, fmt.Errorf("%s: the hidden value cannot be edited in part, enter a new value or clear the field", field)
		}
	}
	if changed {
		var err error
		if patch, err = json.Marshal(body); err != nil {
			return nil, trackerPatchInfo{}, err
		}
	}
	patch, err := applyTrackerLogin(cfg, key, patch)
	if err != nil {
		return nil, trackerPatchInfo{}, err
	}
	return patch, info, nil
}

var errDomainReadOnly = errors.New("domain is read-only")

var trackerJSONFields = func() []string {
	typ := reflect.TypeFor[config.TrackerItem]()
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}()

func hostKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func swapTrackerDomain(cfg config.App, key string, body map[string]json.RawMessage, sent, current string, shown []string) error {
	mirrors := effectiveTrackerMirrors(cfg, key)
	mirrorsSent := false
	if raw, ok := body["mirrors"]; ok && string(raw) != "null" {
		mirrors = nil
		mirrorsSent = true
		if json.Unmarshal(raw, &mirrors) != nil {
			return errDomainReadOnly
		}
	}
	want := hostKey(sent)
	i := slices.IndexFunc(mirrors, func(m string) bool { return hostKey(m) == want })
	if want == "" || i < 0 {
		return errDomainReadOnly
	}
	next := strings.TrimSpace(mirrors[i])
	seen := map[string]bool{want: true}
	rest := make([]string, 0, len(mirrors))
	tail := []string{current}
	// прежний основной диалог показал среди адресов, а в списке его нет: пользователь его убрал
	if mirrorsSent && slices.ContainsFunc(shown, func(h string) bool { return hostKey(h) == hostKey(current) }) {
		tail = nil
	}
	for _, m := range slices.Concat(mirrors, tail) {
		m = strings.TrimSpace(m)
		if m == "" || seen[hostKey(m)] {
			continue
		}
		seen[hostKey(m)] = true
		rest = append(rest, m)
	}

	active := ""
	if it := cfg.Tracker.ByKey(key); it != nil {
		active = it.ActiveDomain
	}
	if raw, ok := body["active_domain"]; ok && string(raw) != "null" {
		// кривой active_domain отклонит валидация патча дальше
		_ = json.Unmarshal(raw, &active)
	}
	// active_domain равный основному домену это явный выбор "только основной, без зеркал",
	// пишем его строкой нового домена
	if hostKey(active) == want {
		body["active_domain"], _ = json.Marshal(next)
	}
	// рантайм переносит адреса входа и загрузки только на active_domain. После замены
	// active пуст или равен новому основному, и вход ушел бы на прежний, часто
	// заблокированный хост. Адреса на постороннем хосте не трогаем
	for field, def := range effectiveTrackerURLs(cfg, key) {
		link := def
		if raw, ok := body[field]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &link) != nil {
				continue
			}
		}
		if u, err := url.Parse(strings.TrimSpace(link)); err == nil && u.Host != "" && hostKey(u.Host) == hostKey(current) {
			body[field], _ = json.Marshal(trackerModel.ReplaceURLHost(strings.TrimSpace(link), next))
		}
	}
	var err error
	if body["domain"], err = json.Marshal(next); err != nil {
		return err
	}
	body["mirrors"], err = json.Marshal(rest)
	return err
}

func keepUnseenMirrors(cfg config.App, key string, body map[string]json.RawMessage, rawSeen json.RawMessage) ([]string, error) {
	if string(rawSeen) == "null" {
		return nil, nil
	}
	var seen []string
	if json.Unmarshal(rawSeen, &seen) != nil {
		return nil, errors.New("mirrors_seen must be a list of hosts")
	}
	raw, ok := body["mirrors"]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var sent []string
	if json.Unmarshal(raw, &sent) != nil {
		// кривой список отклонит валидация патча дальше
		return nil, nil
	}
	stored := effectiveTrackerItem(cfg, key)
	seenKeys := make(map[string]bool, len(seen))
	for _, m := range seen {
		seenKeys[hostKey(m)] = true
	}
	inMirrors := make(map[string]bool, len(stored.Mirrors))
	for _, m := range stored.Mirrors {
		inMirrors[hostKey(m)] = true
	}
	retired := make(map[string]string, len(stored.Retired))
	for _, m := range stored.Retired {
		retired[hostKey(m)] = strings.TrimSpace(m)
	}
	// выбор хоста основным это подтверждение, без него замена домена дальше отклонит патч
	var mainDomain string
	if raw, ok := body["domain"]; ok {
		_ = json.Unmarshal(raw, &mainDomain)
	}
	delete(retired, hostKey(mainDomain))
	// хост, который диалог показал, а синхронизация зеркал убрала после его открытия,
	// не воскрешаем. Заново введенный вручную (его нет в mirrors_seen) возвращается
	var removed []string
	kept := make([]string, 0, len(sent))
	for _, m := range sent {
		k := hostKey(m)
		if old, ok := retired[k]; ok && seenKeys[k] && !inMirrors[k] {
			if !slices.Contains(removed, old) {
				removed = append(removed, old)
			}
			continue
		}
		kept = append(kept, m)
	}
	// как и задача синхронизации: активным адресом убранный хост не остается
	if raw, ok := body["active_domain"]; ok && len(removed) > 0 {
		var active string
		if json.Unmarshal(raw, &active) == nil && slices.ContainsFunc(removed, func(h string) bool { return hostKey(h) == hostKey(active) }) {
			body["active_domain"] = json.RawMessage(`""`)
		}
	}
	// диалог, открытый до записи синхронизации зеркал, не видел добавленных ею хостов,
	// и Save молча убрал бы их
	known := make(map[string]bool, len(seen)+len(kept))
	for _, m := range slices.Concat(seen, kept) {
		known[hostKey(m)] = true
	}
	changed := len(removed) > 0
	for _, m := range stored.Mirrors {
		m = strings.TrimSpace(m)
		if m == "" || known[hostKey(m)] {
			continue
		}
		known[hostKey(m)] = true
		kept = append(kept, m)
		changed = true
	}
	if !changed {
		return nil, nil
	}
	var err error
	body["mirrors"], err = json.Marshal(kept)
	return removed, err
}

func recomputeMirrorLists(cfg config.App, key string, body map[string]json.RawMessage, shown map[string]bool) (bool, []string) {
	stored := effectiveTrackerItem(cfg, key)
	domain, mirrors, active := stored.Domain, stored.Mirrors, stored.ActiveDomain
	for field, dst := range map[string]any{"domain": &domain, "mirrors": &mirrors, "active_domain": &active} {
		if raw, ok := body[field]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, dst) != nil {
				return false, nil
			}
		}
	}
	next := make(map[string]bool, 1+len(mirrors))
	for _, h := range slices.Concat([]string{domain}, mirrors) {
		next[hostKey(h)] = true
	}
	seen := make(map[string]bool)
	retired := make([]string, 0, len(stored.Retired))
	for _, h := range slices.Concat(stored.Retired, []string{stored.Domain}, stored.Mirrors) {
		h = strings.TrimSpace(h)
		if h == "" || next[hostKey(h)] || seen[hostKey(h)] {
			continue
		}
		seen[hostKey(h)] = true
		retired = append(retired, h)
	}
	inMirrors := make(map[string]bool, len(mirrors))
	for _, h := range mirrors {
		inMirrors[hostKey(h)] = true
	}
	// выбор хоста основным или активным адресом и есть подтверждение
	delete(inMirrors, hostKey(domain))
	delete(inMirrors, hostKey(active))
	// подтверждает только хост, который диалог показал отмеченным и пользователь оставил
	for k := range shown {
		delete(inMirrors, k)
	}
	unconfirmed := make([]string, 0, len(stored.Unconfirmed))
	var confirmed []string
	for _, h := range stored.Unconfirmed {
		h = strings.TrimSpace(h)
		switch k := hostKey(h); {
		case h == "":
		case inMirrors[k]:
			delete(inMirrors, k)
			unconfirmed = append(unconfirmed, h)
		case next[k] || k == hostKey(active):
			confirmed = append(confirmed, h)
		}
	}
	changed := false
	if !slices.Equal(retired, stored.Retired) {
		body["retired"], _ = json.Marshal(retired)
		changed = true
	}
	if !slices.Equal(unconfirmed, stored.Unconfirmed) {
		body["unconfirmed"], _ = json.Marshal(unconfirmed)
		changed = true
	}
	return changed, confirmed
}

func orderTrackerKeys(all, saved []string) []string {
	known := make(map[string]struct{}, len(all))
	for _, key := range all {
		known[key] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, key := range saved {
		if _, ok := known[key]; ok {
			out = append(out, key)
			delete(known, key)
		}
	}
	for _, key := range all {
		if _, ok := known[key]; ok {
			out = append(out, key)
		}
	}
	return out
}

func (s *Server) savedTrackerOrder(ctx context.Context) []string {
	raw, ok, err := s.db.GetSetting(ctx, sqlite.SettingTrackersOrder)
	if err != nil {
		logger.Warn("read trackers order: " + err.Error())
		return nil
	}
	if !ok {
		return nil
	}
	var order []string
	if err := json.Unmarshal(raw, &order); err != nil {
		// порядок это косметика, битый документ не должен ронять страницу
		logger.Warn("trackers order: " + err.Error())
		return nil
	}
	return order
}

func (s *Server) storedTrackerList(ctx context.Context) ([]string, error) {
	added, ok, err := settings.TrackersAdded(ctx, s.db)
	if err != nil {
		return nil, err
	}
	// без документа видны все трекеры, как до появления списка
	if !ok {
		return registry.Keys(), nil
	}
	return added, nil
}

func (s *Server) addedTrackerKeys(ctx context.Context) ([]string, error) {
	list, err := s.storedTrackerList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, key := range list {
		if trackerKnown(key) {
			out = append(out, key)
		}
	}
	return out, nil
}

func (s *Server) trackerAdded(ctx context.Context, key string) (bool, error) {
	added, err := s.addedTrackerKeys(ctx)
	if err != nil {
		return false, err
	}
	return slices.Contains(added, key), nil
}

func trackerView(key string, it config.TrackerItem) trackerResp {
	tmpl, login, password, supported := trackerCredsView(key, it)
	password = settings.MaskSecret(password)
	if supported {
		// в шаблоне вместо логина и пароля плейсхолдеры, его можно показывать
		it.Credentials = tmpl
	} else {
		it.Credentials = settings.MaskSecret(it.Credentials)
	}
	it.Cookies = settings.MaskSecret(it.Cookies)
	return trackerResp{
		ID: registry.ID(key), Key: key,
		Login: login, Password: password, SupportsLogin: supported,
		TrackerItem: it,
	}
}

func effectiveTrackerDomain(cfg config.App, key string) string {
	if it := cfg.Tracker.ByKey(key); it != nil {
		return it.Domain
	}
	def, _ := registry.ByKey(key)
	return def.Defaults.Domain
}

func effectiveTrackerURLs(cfg config.App, key string) map[string]string {
	it := cfg.Tracker.ByKey(key)
	if it == nil {
		def, _ := registry.ByKey(key)
		it = &def.Defaults
	}
	return map[string]string{"login_url": it.LoginURL, "download_url": it.DownloadURL}
}

func effectiveTrackerItem(cfg config.App, key string) config.TrackerItem {
	if it := cfg.Tracker.ByKey(key); it != nil {
		return *it
	}
	def, _ := registry.ByKey(key)
	return def.Defaults
}

func effectiveTrackerMirrors(cfg config.App, key string) []string {
	if it := cfg.Tracker.ByKey(key); it != nil {
		return it.Mirrors
	}
	def, _ := registry.ByKey(key)
	return def.Defaults.Mirrors
}

func withTrackerEnabled(patch json.RawMessage) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(patch, &body); err != nil {
		return nil, err
	}
	if raw, ok := body["enabled"]; ok && string(raw) != "null" {
		return patch, nil
	}
	body["enabled"] = json.RawMessage("true")
	return json.Marshal(body)
}
