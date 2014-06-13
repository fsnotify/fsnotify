// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fsnotify implements file system notification.
package fsnotify

import "fmt"

// Add starts watching for operations on the named file.
func (w *Watcher) Add(path string) error {
	return w.watch(path)
}

// Remove stops watching for operations on the named file.
func (w *Watcher) Remove(path string) error {
	return w.removeWatch(path)
}

// String formats the event e in the form
// "filename: DELETE|MODIFY|..."
func (e *Event) String() string {
	var events string = ""

	if e.IsCreate() {
		events += "|" + "CREATE"
	}

	if e.IsDelete() {
		events += "|" + "DELETE"
	}

	if e.IsModify() {
		events += "|" + "MODIFY"
	}

	if e.IsRename() {
		events += "|" + "RENAME"
	}

	if e.IsAttrib() {
		events += "|" + "ATTRIB"
	}

	if len(events) > 0 {
		events = events[1:]
	}

	return fmt.Sprintf("%q: %s", e.Name, events)
}
