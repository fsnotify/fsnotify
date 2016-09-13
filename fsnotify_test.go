// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !plan9

package fsnotify

import "testing"

func TestEventStringWithValue(t *testing.T) {
	expectedString := `"/usr/someFile": CREATE|CHMOD`
	event := Event{Name: "/usr/someFile", Op: Chmod | Create}
	if event.String() != expectedString {
		t.Fatalf("Expected %s, got: %v", expectedString, event.String())
	}
}

func TestEventOpStringWithValue(t *testing.T) {
	expectedOpString := "WRITE|CHMOD"
	event := Event{Name: "someFile", Op: Write | Chmod}
	if event.Op.String() != expectedOpString {
		t.Fatalf("Expected %s, got: %v", expectedOpString, event.Op.String())
	}
}

func TestEventOpStringWithNoValue(t *testing.T) {
	expectedOpString := ""
	event := Event{Name: "testFile", Op: 0}
	if event.Op.String() != expectedOpString {
		t.Fatalf("Expected %s, got: %v", expectedOpString, event.Op.String())
	}
}
