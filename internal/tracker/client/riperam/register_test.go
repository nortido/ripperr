package riperam

import (
	"testing"

	"ripperr/internal/tracker/registry"
)

func TestRegistered(t *testing.T) {
	d, ok := registry.ByKey("riperam")
	if !ok || d.ID != 9 {
		t.Fatalf("riperam not registered with id 9: %+v", d)
	}
	if d.Defaults.Enabled {
		t.Error("riperam must ship disabled")
	}
	if d.Defaults.Publicity != "private" || d.Defaults.Domain != "riperam.org" {
		t.Errorf("defaults: %+v", d.Defaults)
	}
	if d.Login == nil || d.Login.UserField != "username" || d.Login.PassField != "password" {
		t.Errorf("login form: %+v", d.Login)
	}
}
