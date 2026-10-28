package model

import (
	"errors"
	"fmt"
	"testing"
)

func TestNoSpaceError(t *testing.T) {
	src := &NoSpaceError{Path: "/d", Need: 3<<30 + 1<<29, Free: 1 << 30, Hash: "abc"}
	err := fmt.Errorf("x: %w", src)
	if !errors.Is(err, ErrNoSpace) {
		t.Error("errors.Is(err, ErrNoSpace) = false")
	}
	var ns *NoSpaceError
	if !errors.As(err, &ns) {
		t.Fatal("errors.As to *NoSpaceError failed")
	}
	if ns.Need != 3<<30+1<<29 || ns.Free != 1<<30 || ns.Hash != "abc" {
		t.Errorf("fields = %+v", ns)
	}
	if want := "not enough free disk space in /d: need 3.5 GB, free 1.0 GB"; src.Error() != want {
		t.Errorf("Error() = %q, want %q", src.Error(), want)
	}
	if errors.Is(ErrFreeSpaceUnknown, ErrNoSpace) {
		t.Error("ErrFreeSpaceUnknown must differ from ErrNoSpace")
	}
}
