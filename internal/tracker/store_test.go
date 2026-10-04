package tracker

import (
	"context"
	"errors"
	"net/http"
	"testing"

	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
)

type storeFakeDB struct {
	row   *trackerModel.Tracker
	saved []trackerModel.Tracker
}

func (d *storeFakeDB) GetTracker(context.Context, int) (*trackerModel.Tracker, error) {
	cp := *d.row
	return &cp, nil
}

func (d *storeFakeDB) SaveTracker(_ context.Context, t *trackerModel.Tracker) error {
	d.saved = append(d.saved, *t)
	return nil
}

type storeFakeClient struct {
	client.IClient
	creds, publicity string
	cookies          []*http.Cookie
	initErr          error
	gotPayload       trackerModel.Tracker
	gotForce         bool
	initCalls        int
}

func (c *storeFakeClient) GetCredentials() string  { return c.creds }
func (c *storeFakeClient) GetPublicity() string    { return c.publicity }
func (c *storeFakeClient) Cookies() []*http.Cookie { return c.cookies }

func (c *storeFakeClient) InitClient(_ context.Context, p *trackerModel.Tracker, force bool) error {
	c.initCalls++
	c.gotPayload = *p
	c.gotForce = force
	return c.initErr
}

func uidCookie(v string) []*http.Cookie { return []*http.Cookie{{Name: "uid", Value: v}} }

func TestStoreUsesConfigCredentialsAndForcesRelogin(t *testing.T) {
	db := &storeFakeDB{row: &trackerModel.Tracker{Exists: true, Credentials: "login=old", Publicity: "public", Cookies: uidCookie("old")}}
	tc := &storeFakeClient{creds: "login=new", publicity: "private", cookies: uidCookie("new")}
	s := &service{db: db}

	if err := s.store(context.Background(), 1, tc, false); err != nil {
		t.Fatalf("store: %v", err)
	}
	if tc.gotPayload.Credentials != "login=new" || tc.gotPayload.Publicity != "private" {
		t.Fatalf("InitClient payload creds=%q publicity=%q, want login=new/private", tc.gotPayload.Credentials, tc.gotPayload.Publicity)
	}
	if !tc.gotForce {
		t.Fatal("changed credentials must force a real relogin")
	}
	if len(db.saved) != 1 {
		t.Fatalf("SaveTracker calls = %d, want 1", len(db.saved))
	}
	if db.saved[0].Credentials != "login=new" || len(db.saved[0].Cookies) != 1 || db.saved[0].Cookies[0].Value != "new" {
		t.Fatalf("saved creds=%q cookies=%v", db.saved[0].Credentials, db.saved[0].Cookies)
	}
}

func TestStoreKeepsRowWhenCredentialsEqual(t *testing.T) {
	db := &storeFakeDB{row: &trackerModel.Tracker{Exists: true, Credentials: "login=same", Publicity: "private", Cookies: uidCookie("c")}}
	tc := &storeFakeClient{creds: "login=same", publicity: "private", cookies: uidCookie("c")}
	s := &service{db: db}

	if err := s.store(context.Background(), 1, tc, false); err != nil {
		t.Fatalf("store: %v", err)
	}
	if tc.gotForce {
		t.Fatal("forceLogin must pass through unchanged when credentials are equal")
	}
	if len(db.saved) != 0 {
		t.Fatalf("SaveTracker calls = %d, want 0", len(db.saved))
	}
}

func TestStoreDoesNotSaveChangedCredentialsOnLoginFailure(t *testing.T) {
	db := &storeFakeDB{row: &trackerModel.Tracker{Exists: true, Credentials: "login=old", Publicity: "private", Cookies: uidCookie("old")}}
	loginErr := errors.New("login failed")
	tc := &storeFakeClient{creds: "login=new", publicity: "private", cookies: uidCookie("old"), initErr: loginErr}
	s := &service{db: db}

	if err := s.store(context.Background(), 1, tc, false); !errors.Is(err, loginErr) {
		t.Fatalf("store err = %v, want %v", err, loginErr)
	}
	if len(db.saved) != 0 {
		t.Fatalf("SaveTracker calls = %d, want 0", len(db.saved))
	}
}

func TestStoreKeepsRowCredentialsWhenConfigEmpty(t *testing.T) {
	db := &storeFakeDB{row: &trackerModel.Tracker{Exists: true, Credentials: "login=old", Publicity: "private", Cookies: uidCookie("c")}}
	tc := &storeFakeClient{creds: "", publicity: "private", cookies: uidCookie("c")}
	s := &service{db: db}

	if err := s.store(context.Background(), 1, tc, false); err != nil {
		t.Fatalf("store: %v", err)
	}
	if tc.gotPayload.Credentials != "login=old" {
		t.Fatalf("InitClient payload creds=%q, want login=old", tc.gotPayload.Credentials)
	}
	if tc.gotForce {
		t.Fatal("empty config credentials must not force a relogin")
	}
	for _, sv := range db.saved {
		if sv.Credentials == "" {
			t.Fatal("row credentials were wiped")
		}
	}
}
