// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build aix

// empty implementation, just to allow build

package fsnotify

import (
	"errors"
	"os"
	"sync"
	"time"
)

const (
	sleepTime time.Duration = 50 * time.Millisecond
)

var (
	// ErrWatchedFileDeleted ...
	ErrWatchedFileDeleted = errors.New("error: watched file or folder deleted")
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events    chan Event
	Errors    chan error
	mu        *sync.Mutex   // Map access
	closed    chan struct{} // Channel to respond to Close
	close     chan struct{}
	wg        *sync.WaitGroup
	running   bool
	names     map[string]bool        // bool for recursive or not.
	files     map[string]os.FileInfo // map of files.
	ops       map[Op]struct{}        // Op filtering.
	maxEvents int
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	// Set up the WaitGroup for w.Wait().
	var wg sync.WaitGroup
	wg.Add(1)

	w := &Watcher{
		Events: make(chan Event),
		Errors: make(chan error),
		closed: make(chan struct{}),
		close:  make(chan struct{}),
		mu:     new(sync.Mutex),
		wg:     &wg,
		files:  make(map[string]os.FileInfo),
		names:  make(map[string]bool),
	}
	go w.readEvents()
	return w, nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	return errors.New("Not implemented")
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	return errors.New("Not implemented")
}

// Remove stops watching the the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	return errors.New("Not implemented")
}

func (w *Watcher) list(name string) (map[string]os.FileInfo, error) {
	return nil, errors.New("Not implemented")
}

func (w *Watcher) retrieveFileList() map[string]os.FileInfo {
	return nil
}

func (w *Watcher) readEvents() {
	return
}

func (w *Watcher) pollEvents(files map[string]os.FileInfo, evt chan Event,
	cancel chan struct{}) {
	return
}

// Remove removes either a single file or a directory recursively from
// the file's list.
func (w *Watcher) removeRecursive(name string) (err error) {
	return errors.New("Not implemented")
}

func (w *Watcher) listRecursive(name string) (map[string]os.FileInfo, error) {
	return nil, errors.New("Not implemented")
}
