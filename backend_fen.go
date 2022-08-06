//go:build solaris
// +build solaris

package fsnotify

import (
	"errors"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	return nil, errors.New("FEN based watcher not yet supported for fsnotify\n")
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	return nil
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	return nil
}

// Remove stops watching the the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	return nil
}
