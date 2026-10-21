package transmission

import (
	"context"
	"errors"
	"testing"

	"ripperr/internal/model"
)

func TestSetCommentUnsupported(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})
	before := len(stub.methods)
	err := c.SetComment(context.Background(), "abc", "https://x/1")
	if !errors.Is(err, model.ErrCommentUnsupported) {
		t.Fatalf("err = %v, want ErrCommentUnsupported", err)
	}
	if len(stub.methods) != before {
		t.Fatalf("RPC calls %v, want none", stub.methods[before:])
	}
}

func TestExportTorrentUnsupported(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})
	if _, err := c.ExportTorrent(context.Background(), "abc"); !errors.Is(err, model.ErrExportUnsupported) {
		t.Fatalf("err = %v, want ErrExportUnsupported", err)
	}
}
