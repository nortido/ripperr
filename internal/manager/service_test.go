package manager

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"trackerr/internal/model"
)

type stubClient struct {
	calls             []string
	detName           string
	detCom            string
	detErr            error
	addOpts           model.ManagerAddFileOptions
	incomingSame      bool
	incomingKnown     bool
	addErr            error
	blackholeWriteErr error
}

func (s *stubClient) IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (bool, bool) {
	s.calls = append(s.calls, "incoming")
	if existing == "" {
		return false, false
	}
	if s.incomingKnown {
		return s.incomingSame, true
	}
	return true, true
}

func (s *stubClient) List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	return nil, nil
}

func (s *stubClient) GetTorrentDetails(ctx context.Context, hash string) (string, string, error) {
	s.calls = append(s.calls, "details")
	return s.detName, s.detCom, s.detErr
}

func (s *stubClient) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "add")
	s.addOpts = opts
	return s.addErr
}

func (s *stubClient) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "add")
	s.addOpts = opts
	return s.addErr
}

func (s *stubClient) Delete(ctx context.Context, opts model.ManagerDeleteOptions) error {
	s.calls = append(s.calls, "delete")
	return nil
}

func (s *stubClient) WriteBlackholePair(data []byte, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "blackhole-write")
	return s.blackholeWriteErr
}

func (s *stubClient) RemoveBlackholePair(hash string) {
	s.calls = append(s.calls, "blackhole-remove")
}

func TestSubmit_PreservesNameAndCommentFromExistingTorrent(t *testing.T) {
	stub := &stubClient{detName: "existing name", detCom: "existing comment"}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "abc", Name: "new name"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stub.addOpts.Name != "existing name" {
		t.Errorf("expected name preserved from existing torrent, got %q", stub.addOpts.Name)
	}
	if stub.addOpts.Comment != "existing comment" {
		t.Errorf("expected comment filled from existing torrent, got %q", stub.addOpts.Comment)
	}
	wantOrder := []string{"details", "incoming", "blackhole-write", "delete", "add", "blackhole-remove"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_NoHashSkipsDetailsLookup(t *testing.T) {
	stub := &stubClient{}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Name: "some name", Comment: "some comment"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stub.addOpts.Name != "some name" || stub.addOpts.Comment != "some comment" {
		t.Errorf("expected caller-provided opts unchanged, got %+v", stub.addOpts)
	}
	wantOrder := []string{"incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmitMagnet_PreservesNameAndCommentAndOrder(t *testing.T) {
	stub := &stubClient{detName: "existing name", detCom: "existing comment"}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "abc"}
	if err := m.SubmitMagnet(context.Background(), "magnet:?xt=urn:btih:X", opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stub.addOpts.Name != "existing name" || stub.addOpts.Comment != "existing comment" {
		t.Errorf("expected name/comment preserved, got %+v", stub.addOpts)
	}
	wantOrder := []string{"details", "incoming", "delete", "add"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_DifferentHashAddsBeforeDelete(t *testing.T) {
	stub := &stubClient{incomingSame: false, incomingKnown: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "oldhash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_DifferentHashAddFailureSkipsDelete(t *testing.T) {
	stub := &stubClient{incomingSame: false, incomingKnown: true, addErr: fmt.Errorf("add failed")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "oldhash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err == nil {
		t.Fatal("expected add error")
	}
	for _, call := range stub.calls {
		if call == "delete" {
			t.Fatalf("delete must not run when add fails, calls = %v", stub.calls)
		}
	}
}

func TestSubmit_SameHashBlackholesBeforeDeleteAndRemovesAfterAdd(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "blackhole-write", "delete", "add", "blackhole-remove"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_SameHashBlackholeWriteFailureAbortsReplace(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true, blackholeWriteErr: fmt.Errorf("disk full")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err == nil {
		t.Fatal("expected blackhole write error")
	}
	wantOrder := []string{"details", "incoming", "blackhole-write"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (no delete, no add)", stub.calls, wantOrder)
	}
}

func TestSubmit_SameHashAddFailureLeavesBlackholePair(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true, addErr: fmt.Errorf("add failed")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err == nil {
		t.Fatal("expected add error")
	}
	wantOrder := []string{"details", "incoming", "blackhole-write", "delete", "add"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (pair left on disk, no remove)", stub.calls, wantOrder)
	}
}

func TestSubmit_DifferentHashWritesNoBlackholePair(t *testing.T) {
	stub := &stubClient{incomingSame: false, incomingKnown: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "oldhash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (add-first branch never blackholes)", stub.calls, wantOrder)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
