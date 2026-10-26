package manager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ripperr/internal/model"
)

func assertNoClientChange(t *testing.T, stub *stubClient) {
	t.Helper()
	for _, c := range []string{"blackhole-remove", "discard", "delete", "wait-absent"} {
		if slices.Contains(stub.calls, c) {
			t.Errorf("calls = %v, want no %q", stub.calls, c)
		}
	}
	if len(stub.deleteOpts) != 0 || len(stub.discardHashes) != 0 {
		t.Errorf("delete %v, discard %v, want none", stub.deleteOpts, stub.discardHashes)
	}
}

func TestRemoveDiscardsWithoutFilesWhenDeleteDisabled(t *testing.T) {
	stub := &stubClient{deleteEnabled: false, waitAbsent: true}
	accepted, err := NewManager(stub).Remove(context.Background(), []model.ManagerDeleteOptions{{Hash: "aa", Name: "A", Category: "tv"}})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !equalStrings(accepted, []string{"aa"}) {
		t.Errorf("accepted = %v, want [aa]", accepted)
	}
	assertCalls(t, stub, "blackhole-remove", "discard", "wait-absent")
	if len(stub.deleteOpts) != 0 {
		t.Errorf("deleteOpts = %v, want none", stub.deleteOpts)
	}
	if stub.removeHash != "aa" || !equalStrings(stub.discardHashes, []string{"aa"}) || stub.waitHash != "aa" {
		t.Errorf("pair %q, discard %v, wait %q, want aa", stub.removeHash, stub.discardHashes, stub.waitHash)
	}
}

func TestRemoveWithFilesUsesDelete(t *testing.T) {
	stub := &stubClient{deleteEnabled: true, deletesFiles: true, waitAbsent: true}
	target := model.ManagerDeleteOptions{Hash: "aa", Name: "A", Category: "tv", DeleteFiles: true}
	accepted, err := NewManager(stub).Remove(context.Background(), []model.ManagerDeleteOptions{target})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !equalStrings(accepted, []string{"aa"}) {
		t.Errorf("accepted = %v, want [aa]", accepted)
	}
	assertCalls(t, stub, "blackhole-remove", "delete", "wait-absent")
	if len(stub.deleteOpts) != 1 || stub.deleteOpts[0] != target {
		t.Errorf("deleteOpts = %v, want [%v]", stub.deleteOpts, target)
	}
	if len(stub.discardHashes) != 0 {
		t.Errorf("discard %v, want none", stub.discardHashes)
	}
}

func TestRemoveRefusesBeforeAnyChange(t *testing.T) {
	tests := []struct {
		name    string
		stub    *stubClient
		targets []model.ManagerDeleteOptions
		want    error
	}{
		{"empty hash", &stubClient{waitAbsent: true}, []model.ManagerDeleteOptions{{Hash: "aa"}, {Hash: ""}}, nil},
		{"pending replace", &stubClient{waitAbsent: true, pending: map[string]bool{"bb": true}}, []model.ManagerDeleteOptions{{Hash: "aa"}, {Hash: "bb"}}, model.ErrReplacePending},
		{"files not allowed", &stubClient{waitAbsent: true, deleteEnabled: true, deletesFilesSeq: []bool{true, false}}, []model.ManagerDeleteOptions{{Hash: "aa", DeleteFiles: true}, {Hash: "bb", DeleteFiles: true}}, model.ErrFilesNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted, err := NewManager(tt.stub).Remove(context.Background(), tt.targets)
			if err == nil {
				t.Fatal("Remove error = nil, want refusal")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("Remove error = %v, want %v", err, tt.want)
			}
			if accepted != nil {
				t.Errorf("accepted = %v, want nil", accepted)
			}
			assertNoClientChange(t, tt.stub)
		})
	}
}

func TestRemoveUnconfirmed(t *testing.T) {
	stub := &stubClient{waitAbsent: false}
	accepted, err := NewManager(stub).Remove(context.Background(), []model.ManagerDeleteOptions{{Hash: "aa", Name: "A"}})
	if !errors.Is(err, model.ErrRemoveUnconfirmed) || !strings.Contains(err.Error(), "aa") {
		t.Fatalf("Remove error = %v, want ErrRemoveUnconfirmed naming aa", err)
	}
	if !equalStrings(accepted, []string{"aa"}) {
		t.Errorf("accepted = %v, want [aa]", accepted)
	}
}

func TestRemoveDiscardErrorAbsentCounts(t *testing.T) {
	stub := &stubClient{discardErr: fmt.Errorf("timeout"), exists: false, waitAbsent: true}
	accepted, err := NewManager(stub).Remove(context.Background(), []model.ManagerDeleteOptions{{Hash: "aa"}})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !equalStrings(accepted, []string{"aa"}) {
		t.Errorf("accepted = %v, want [aa]", accepted)
	}
	if !slices.Contains(stub.calls, "exists") {
		t.Errorf("calls = %v, want exists probe", stub.calls)
	}
}

func TestRemoveDiscardErrorPresentStops(t *testing.T) {
	tests := []struct {
		name string
		stub *stubClient
	}{
		{"present", &stubClient{exists: true, waitAbsent: true}},
		{"probe failed", &stubClient{existsErr: fmt.Errorf("connection refused"), waitAbsent: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discardErr := fmt.Errorf("timeout")
			tt.stub.discardErr = discardErr
			accepted, err := NewManager(tt.stub).Remove(context.Background(), []model.ManagerDeleteOptions{{Hash: "aa"}, {Hash: "bb"}})
			if !errors.Is(err, discardErr) {
				t.Fatalf("Remove error = %v, want %v", err, discardErr)
			}
			if len(accepted) != 0 {
				t.Errorf("accepted = %v, want empty", accepted)
			}
			if !equalStrings(tt.stub.discardHashes, []string{"aa"}) {
				t.Errorf("discard %v, want [aa]", tt.stub.discardHashes)
			}
		})
	}
}

func TestAddUsesAddFromBufferOnly(t *testing.T) {
	data := []byte("d8:announce0:e")
	opts := model.ManagerAddFileOptions{Category: "tv", Tag: "t", AutoManaged: true, Comment: "https://tracker.example/t/1"}
	addErr := fmt.Errorf("add failed")
	stub := &stubClient{addErr: addErr}
	if err := NewManager(stub).Add(context.Background(), data, opts); !errors.Is(err, addErr) {
		t.Fatalf("Add error = %v, want %v", err, addErr)
	}
	if string(stub.addData) != string(data) {
		t.Errorf("addData = %q, want %q", stub.addData, data)
	}
	if !reflect.DeepEqual(stub.addOpts, opts) {
		t.Errorf("addOpts = %+v, want %+v", stub.addOpts, opts)
	}
	assertCalls(t, stub, "add")
}

func TestManagerDeletesFilesDelegates(t *testing.T) {
	for _, want := range []bool{true, false} {
		stub := &stubClient{deletesFiles: want}
		if got := NewManager(stub).DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: "aa", Category: "tv"}); got != want {
			t.Errorf("DeletesFiles = %v, want %v", got, want)
		}
		if stub.deletesFilesCalls != 1 {
			t.Errorf("client DeletesFiles calls = %d, want 1", stub.deletesFilesCalls)
		}
	}
}
