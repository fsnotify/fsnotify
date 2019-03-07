// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !plan9
// +build !plan9

// Package fsnotify provides a platform-independent interface for file system notifications.
package fsnotify

import (
	"errors"
	"fmt"
	"strings"
)

// Event represents a single file system notification.
type Event struct {
	Name string // Relative path to the file or directory.
	Op   Op     // File operation that triggered the event.
}

// Op describes a set of file operations.
type Op uint32

// These are the generalized file operations that can trigger a notification.
const (
	Create Op = 1 << iota
	Write
	Remove
	Rename
	Chmod
)

func (op Op) String() string {
	// Use a builder for efficient string concatenation
	var builder strings.Builder

	if op&Create == Create {
		builder.WriteString("|CREATE")
	}
	if op&Remove == Remove {
		builder.WriteString("|REMOVE")
	}
	if op&Write == Write {
		builder.WriteString("|WRITE")
	}
	if op&Rename == Rename {
		builder.WriteString("|RENAME")
	}
	if op&Chmod == Chmod {
		builder.WriteString("|CHMOD")
	}
	if builder.Len() == 0 {
		return ""
	}
	return builder.String()[1:] // Strip leading pipe
}

// String returns a string representation of the event in the form
// "file: REMOVE|WRITE|..."
func (e Event) String() string {
	return fmt.Sprintf("%q: %s", e.Name, e.Op.String())
}

// Common errors that can be reported by a watcher
var (
	ErrNonExistentWatch = errors.New("can't remove non-existent watcher")
	ErrEventOverflow    = errors.New("fsnotify queue overflow")
)
