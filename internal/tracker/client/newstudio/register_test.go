package newstudio

import (
	"testing"

	"ripperr/internal/tracker/registry"
)

func TestRegistered(t *testing.T) {
	d, ok := registry.ByKey("newstudio")
	if !ok || d.ID != 16 {
		t.Fatalf("newstudio not registered with id 16: %+v", d)
	}
	if d.Defaults.Enabled {
		t.Error("newstudio must ship disabled")
	}
	if d.Defaults.Publicity != "public" || d.Defaults.Domain != "newstudio.tv" {
		t.Errorf("defaults: %+v", d.Defaults)
	}
	if d.Login == nil || d.Login.UserField != "login_username" || d.Login.PassField != "login_password" {
		t.Errorf("login form: %+v", d.Login)
	}
}
