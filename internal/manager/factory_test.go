package manager

import (
	"errors"
	"testing"

	"ripperr/internal/model"
)

func TestNewClientUnreachableHostReturnsConnectionError(t *testing.T) {
	// непривилегированный порт без слушателя: qbit требует непустые креды, чтобы
	// реально сходить в сеть (иначе Login() выходит без запроса), transmission всегда
	// ходит в session-get
	cases := []struct {
		kind string
		cfg  model.ManagerConfig
	}{
		{ClientQbit, model.ManagerConfig{Host: "http://127.0.0.1:1", Username: "u", Password: "p"}},
		{"", model.ManagerConfig{Host: "http://127.0.0.1:1", Username: "u", Password: "p"}},
		{ClientTransmission, model.ManagerConfig{Host: "http://127.0.0.1:1"}},
	}
	for _, c := range cases {
		_, err := NewClient(c.kind, c.cfg)
		if err == nil {
			t.Fatalf("kind=%q: expected connection error, got nil", c.kind)
		}
		if errors.Is(err, ErrUnknownClient) {
			t.Fatalf("kind=%q: got ErrUnknownClient, want a connection error: %v", c.kind, err)
		}
	}
}

func TestNewClientUnknownKind(t *testing.T) {
	_, err := NewClient("bogus", model.ManagerConfig{})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !errors.Is(err, ErrUnknownClient) {
		t.Fatalf("error = %v, want ErrUnknownClient", err)
	}
}

func TestNormalizeClient(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"qbit", "qbit"},
		{"Transmission", "transmission"},
		{"  transmission  ", "transmission"},
		{"QBIT", "qbit"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := NormalizeClient(c.in); got != c.want {
			t.Errorf("NormalizeClient(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewClientMixedCaseKind(t *testing.T) {
	// MANAGER_CLIENT=Transmission с чужим регистром и пробелами из env обязан
	// уйти в ветку transmission, а не в ErrUnknownClient
	_, err := NewClient(" Transmission ", model.ManagerConfig{Host: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected connection error for unreachable host")
	}
	if errors.Is(err, ErrUnknownClient) {
		t.Fatalf("got ErrUnknownClient for a mixed-case known kind: %v", err)
	}
}
