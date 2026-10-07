package animelayer

import (
	"testing"

	"ripperr/internal/tracker/registry"
)

func TestRegistered(t *testing.T) {
	d, ok := registry.ByKey("animelayer")
	if !ok || d.ID != 12 {
		t.Fatalf("animelayer not registered with id 12: %+v", d)
	}
	if d.Defaults.Enabled {
		t.Error("animelayer must ship disabled")
	}
	if d.Defaults.Publicity != "private" || d.Defaults.Domain != "animelayer.ru" {
		t.Errorf("defaults: %+v", d.Defaults)
	}
	if d.Login == nil || d.Login.UserField != "login" || d.Login.PassField != "password" {
		t.Errorf("login form: %+v", d.Login)
	}
}
