// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsnotify

import "fmt"

const (
	FSN_CREATE = 1
	FSN_MODIFY = 2
	FSN_DELETE = 4
	FSN_RENAME = 8

	FSN_ALL = FSN_MODIFY | FSN_DELETE | FSN_RENAME | FSN_CREATE
)

// Purge events from interal chan to external chan if passes filter
func (w *Watcher) purgeEvents() {
	for ev := range w.internalEvent {
		sendEvent := false
		fsnFlags := w.fsnFlags[ev.Name]

		if (fsnFlags&FSN_CREATE == FSN_CREATE) && ev.IsCreate() {
			sendEvent = true
		}

		if (fsnFlags&FSN_MODIFY == FSN_MODIFY) && ev.IsModify() {
			sendEvent = true
		}

		if (fsnFlags&FSN_DELETE == FSN_DELETE) && ev.IsDelete() {
			sendEvent = true
		}

		if (fsnFlags&FSN_RENAME == FSN_RENAME) && ev.IsRename() {
			//w.RemoveWatch(ev.Name)
			sendEvent = true
		}

		if sendEvent {
			w.Event <- ev
		}
	}

	close(w.Event)
}

// Watch a given file path
func (w *Watcher) Watch(path string) error {
	w.fsnFlags[path] = FSN_ALL
	return w.watch(path)
}

// Watch a given file path for a particular set of notifications (FSN_MODIFY etc.)
func (w *Watcher) WatchFlags(path string, flags uint32) error {
	w.fsnFlags[path] = flags
	return w.watch(path)
}

// Remove a watch on a file
func (w *Watcher) RemoveWatch(path string) error {
	delete(w.fsnFlags, path)
	return w.removeWatch(path)
}

// String formats the event e in the form
// "filename: DELETE|MODIFY|..."
func (e *FileEvent) String() string {
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

	if len(events) > 0 {
		events = events[1:]
	}

	return fmt.Sprintf("%q: %s", e.Name, events)
}
