// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build plan9

package fsnotify

import "syscall"

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	return nil, syscall.EPLAN9
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	return syscall.EPLAN9
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	return syscall.EPLAN9
}

// Remove stops watching the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	return syscall.EPLAN9
}
