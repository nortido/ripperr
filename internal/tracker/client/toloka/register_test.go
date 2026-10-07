package toloka

import (
	"testing"

	"ripperr/internal/tracker/registry"
)

func TestRegistered(t *testing.T) {
	d, ok := registry.ByKey("toloka")
	if !ok || d.ID != 7 {
		t.Fatalf("toloka not registered with id 7: %+v", d)
	}
	if d.Defaults.Enabled {
		t.Error("toloka must ship disabled")
	}
	if d.Defaults.Publicity != "private" || d.Defaults.Domain != "toloka.to" {
		t.Errorf("defaults: %+v", d.Defaults)
	}
	if d.Login == nil || d.Login.UserField != "username" || d.Login.PassField != "password" {
		t.Errorf("login form: %+v", d.Login)
	}
}
