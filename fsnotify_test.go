// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !plan9,!solaris,!windows

// Package fsnotify provides a platform-independent interface for file system notifications.
package fsnotify

import (
	"testing"
)

func TestOpStringify(t *testing.T) {
	if Create.String() != "Create" {
		t.Error("Stringify failed for: Create Op.")
	}
	if Write.String() != "Write" {
		t.Error("Stringify failed for: Write Op.")
	}
	if Remove.String() != "Remove" {
		t.Error("Stringify failed for: Remove Op.")
	}
	if Rename.String() != "Rename" {
		t.Error("Stringify failed for: Rename Op.")
	}
	if Chmod.String() != "Chmod" {
		t.Error("Stringify failed for: Chmod Op.")
	}
}

func TestEventStringify(t *testing.T) {
	e0 := &Event{"foo0", 0}
	if s1, s2 := `"foo0": nil`, e0.String(); s1 != s2 {
		t.Errorf("Stringify expected: `%s`; got: `%s`.", s1, s2)
	}
	e1 := &Event{"foo1", Chmod}
	if s1, s2 := `"foo1": Chmod`, e1.String(); s1 != s2 {
		t.Errorf("Stringify expected: `%s`; got: `%s`.", s1, s2)
	}
	e2 := &Event{"foo2", Write + Chmod}
	if s1, s2 := `"foo2": Write|Chmod`, e2.String(); s1 != s2 {
		t.Errorf("Stringify expected: `%s`; got: `%s`.", s1, s2)
	}
	e3 := &Event{"foo3", Create + Write + Remove + Rename + Chmod}
	if s1, s2 := `"foo3": Create|Write|Remove|Rename|Chmod`, e3.String(); s1 != s2 {
		t.Errorf("Stringify expected: `%s`; got: `%s`.", s1, s2)
	}
}
