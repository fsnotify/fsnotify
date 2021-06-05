// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build plan9

package fsnotify

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func dirStat(s string) (*syscall.Dir, error) {
	fi, err := os.Stat(s)
	if err != nil {
		return nil, os.ErrNotExist
	}
	d, ok := fi.Sys().(*syscall.Dir)
	if !ok || d == nil {
		return nil, fmt.Errorf("could not cast %s to dir", s)
	}
	return d, nil
}

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error

	add    chan string
	remove chan string
	exit   chan struct{}
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	w := &Watcher{
		Events: make(chan Event),
		Errors: make(chan error),
		add:    make(chan string),
		remove: make(chan string),
		exit:   make(chan struct{}),
	}
	go w.watch()
	return w, nil
}

func (w *Watcher) watch() {
	state := make(map[string]*syscall.Dir)
	tick := time.Tick(time.Second)
	for {
		select {
		case s := <-w.add:
			d, err := dirStat(s)
			if err != nil {
				w.Errors <- err
				continue
			}
			state[s] = d
		case s := <-w.remove:
			delete(state, s)
		case <-tick:
			for s, old := range state {
				d, err := dirStat(s)
				switch {
				case err == os.ErrNotExist:
					w.Events <- Event{s, Remove}
					continue
				case err != nil:
					w.Errors <- err
					continue
				}
				if d.Qid.Path != old.Qid.Path {
					state[s] = d
					w.Events <- Event{s, Create}
					continue
				}
				switch {
				case d.Mode != old.Mode:
					fallthrough
				case d.Uid != old.Uid:
					fallthrough
				case d.Gid != old.Gid:
					state[s] = d
					w.Events <- Event{s, Chmod}
					continue
				}
				if d.Qid.Vers != old.Qid.Vers {
					state[s] = d
					w.Events <- Event{s, Write}
				}
			}
		case <-w.exit:
			return
		}
	}
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	w.add <- name
	return nil
}

// Remove stops watching the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	w.remove <- name
	return nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	w.exit <- struct{}{}
	close(w.Events)
	close(w.Errors)
	return nil
}
