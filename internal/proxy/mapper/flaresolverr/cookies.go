package flaresolverr

import (
	"encoding/json"
	"net/http"
	"time"
)

type Cookies []*http.Cookie

type Cookie struct {
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
	Path     string `json:"path,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Expiry   int64  `json:"expiry,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	SameSite string `json:"sameSite,omitempty"`
}

// MarshalJSON converts http.Cookie values into the FlareSolverr cookie format.
func (c *Cookies) MarshalJSON() ([]byte, error) {
	if len(*c) == 0 {
		return []byte(`""`), nil
	}

	cookies := make([]Cookie, len(*c))
	for i, cs := range *c {
		var sameSite string
		switch cs.SameSite {
		case http.SameSiteStrictMode:
			sameSite = "Strict"
		case http.SameSiteNoneMode:
			sameSite = "None"
		case http.SameSiteLaxMode:
			sameSite = "Lax"
		}
		cookies[i] = Cookie{
			Name:     cs.Name,
			Value:    cs.Value,
			Path:     cs.Path,
			Domain:   cs.Domain,
			HTTPOnly: cs.HttpOnly,
			Secure:   cs.Secure,
			SameSite: sameSite,
		}
		if cs.Expires.Unix() > 0 {
			cookies[i].Expiry = cs.Expires.Unix()
		}
	}

	return json.Marshal(cookies)
}

// UnmarshalJSON parses FlareSolverr cookies into http.Cookie values.
func (c *Cookies) UnmarshalJSON(b []byte) error {
	var cookies []Cookie
	if err := json.Unmarshal(b, &cookies); err != nil {
		return err
	}

	*c = make(Cookies, 0, len(cookies))
	for _, cs := range cookies {
		if cs.Name == "" {
			continue
		}
		ck := &http.Cookie{
			Name:     cs.Name,
			Value:    cs.Value,
			Path:     cs.Path,
			Domain:   cs.Domain,
			Secure:   cs.Secure,
			HttpOnly: cs.HTTPOnly,
		}
		if cs.Expiry > 0 {
			ck.Expires = time.Unix(cs.Expiry, 0)
		}
		switch cs.SameSite {
		case "Strict":
			ck.SameSite = http.SameSiteStrictMode
		case "None":
			ck.SameSite = http.SameSiteNoneMode
		case "Lax":
			ck.SameSite = http.SameSiteLaxMode
		}
		*c = append(*c, ck)
	}

	return nil
}
