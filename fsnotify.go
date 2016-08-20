// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !plan9

// Package fsnotify provides a platform-independent interface for file system notifications.
package fsnotify

import (
	"bytes"
	"fmt"
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

// HasCreate returns true if Event has the Create opcode
func (e Event) HasCreate() bool { return e.Op&Create == Create }

// HasWrite returns true if Event has the Write opcode
func (e Event) HasWrite() bool { return e.Op&Write == Write }

// HasRemove returns true if Event has the Remove opcode
func (e Event) HasRemove() bool { return e.Op&Remove == Remove }

// HasRename returns true if Event has the Rename opcode
func (e Event) HasRename() bool { return e.Op&Rename == Rename }

// HasChmod returns true if Event has the Chmod opcode
func (e Event) HasChmod() bool { return e.Op&Chmod == Chmod }

// String returns a string representation of the event in the form
// "file: REMOVE|WRITE|..."
func (e Event) String() string {
	// Use a buffer for efficient string concatenation
	var buffer bytes.Buffer

	if e.HasCreate() {
		buffer.WriteString("|CREATE")
	}
	if e.HasRemove() {
		buffer.WriteString("|REMOVE")
	}
	if e.HasWrite() {
		buffer.WriteString("|WRITE")
	}
	if e.HasRename() {
		buffer.WriteString("|RENAME")
	}
	if e.HasChmod() {
		buffer.WriteString("|CHMOD")
	}

	// If buffer remains empty, return no event names
	if buffer.Len() == 0 {
		return fmt.Sprintf("%q: ", e.Name)
	}

	// Return a list of event names, with leading pipe character stripped
	return fmt.Sprintf("%q: %s", e.Name, buffer.String()[1:])
}
