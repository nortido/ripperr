package web

import (
	"encoding/json"

	"trackerr/internal/config"
	"trackerr/internal/helper/formcreds"
	"trackerr/internal/settings"
	"trackerr/internal/tracker/registry"
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

// trackerLoginFields раскладывает тело формы логина на логин и пароль, чтобы
// интерфейс показал два обычных поля. Трекеры без формы (вход по кукам) отдают
// пустые значения и флаг false: подставлять там некуда.
func trackerLoginFields(key, credentials string) (login, password string, supported bool) {
	def, ok := registry.ByKey(key)
	if !ok || def.Login == nil {
		return "", "", false
	}
	return formcreds.Get(credentials, def.Login.UserField),
		formcreds.Get(credentials, def.Login.PassField), true
}

// applyTrackerLogin вкладывает логин и пароль в строку credentials, оставляя
// остальные её поля нетронутыми: у каждого трекера там свои служебные пары, и
// пересборка строки их бы потеряла. Базой служит строка из тела запроса, если
// она пришла, иначе сохранённая: правка в расширенных настройках должна быть
// последним словом.
func applyTrackerLogin(cfg config.App, key string, patch json.RawMessage) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(patch, &body); err != nil {
		return nil, err
	}
	// JSON null значит "поле не прислали": без этой отсечки Unmarshal отдал бы
	// пустую строку и затёр сохранённый пароль
	rawLogin, hasLogin := body["login"]
	hasLogin = hasLogin && string(rawLogin) != "null"
	rawPassword, hasPassword := body["password"]
	hasPassword = hasPassword && string(rawPassword) != "null"
	_, sentLogin := body["login"]
	_, sentPassword := body["password"]
	if !sentLogin && !sentPassword {
		return patch, nil
	}
	delete(body, "login")
	delete(body, "password")
	if !hasLogin && !hasPassword {
		return json.Marshal(body)
	}

	def, ok := registry.ByKey(key)
	if !ok || def.Login == nil {
		return json.Marshal(body)
	}

	base := ""
	if raw, ok := body["credentials"]; ok {
		if err := json.Unmarshal(raw, &base); err != nil {
			return nil, err
		}
	} else if it := cfg.Tracker.ByKey(key); it != nil {
		base = it.Credentials
	}
	if base == "" {
		base = def.Login.Template
	}

	if hasLogin {
		var login string
		if err := json.Unmarshal(rawLogin, &login); err != nil {
			return nil, err
		}
		base = formcreds.Set(base, def.Login.UserField, login)
	}
	if hasPassword {
		var password string
		if err := json.Unmarshal(rawPassword, &password); err != nil {
			return nil, err
		}
		base = formcreds.Set(base, def.Login.PassField, password)
	}

	encoded, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	body["credentials"] = encoded
	return json.Marshal(body)
}
