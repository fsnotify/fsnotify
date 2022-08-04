// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build solaris
// +build solaris

package fsnotify

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error

	done chan struct{} // Channel for sending a "quit message" to the reader goroutine

	mu   sync.Mutex
	dirs map[string]struct{} // map of explicitly watched directories
	port *unix.EventPort
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	var err error

	w := new(Watcher)
	w.Events = make(chan Event)
	w.Errors = make(chan error)
	w.dirs = make(map[string]struct{})
	w.port, err = unix.NewEventPort()
	if err != nil {
		return nil, err
	}
	w.done = make(chan struct{})

	go w.readEvents()
	return w, nil
}

// sendEvent attempts to send an event to the user, returning true if the event
// was put in the channel successfully and false if the watcher has been closed.
func (w *Watcher) sendEvent(e Event) (sent bool) {
	select {
	case w.Events <- e:
		return true
	case <-w.done:
		return false
	}
}

// sendError attempts to send an error to the user, returning true if the error
// was put in the channel successfully and false if the watcher has been closed.
func (w *Watcher) sendError(err error) (sent bool) {
	select {
	case w.Errors <- err:
		return true
	case <-w.done:
		return false
	}
}

func (w *Watcher) isClosed() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	// Take the lock used by associateFile to prevent
	// lingering events from being processed after the close
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed() {
		return nil
	}
	close(w.done)
	return w.port.Close()
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	if w.isClosed() {
		return errors.New("FEN watcher already closed")
	}
	if w.port.PathIsWatched(name) {
		return nil
	}
	stat, err := os.Stat(name)
	switch {
	case err != nil:
		return err
	case stat.IsDir():
		w.mu.Lock()
		w.dirs[name] = struct{}{}
		w.mu.Unlock()
		return w.handleDirectory(name, stat, w.associateFile)
	default:
		return w.associateFile(name, stat)
	}
}

// Remove stops watching the the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	if w.isClosed() {
		return errors.New("FEN watcher already closed")
	}
	if !w.port.PathIsWatched(name) {
		return fmt.Errorf("can't remove non-existent FEN watch for: %s", name)
	}
	stat, err := os.Stat(name)
	switch {
	case err != nil:
		return err
	case stat.IsDir():
		w.mu.Lock()
		delete(w.dirs, name)
		w.mu.Unlock()
		return w.handleDirectory(name, stat, w.dissociateFile)
	default:
		return w.port.DissociatePath(name)
	}
}

// readEvents contains the main loop that runs in a goroutine watching for events.
func (w *Watcher) readEvents() {
	// If this function returns, the watcher has been closed and we can
	// close these channels
	defer close(w.Errors)
	defer close(w.Events)

	pevents := make([]unix.PortEvent, 8, 8)
	for {
		count, err := w.port.Get(pevents, 1, nil)
		if err != nil && err != unix.ETIME {
			// Interrupted system call (count should be 0) ignore and continue
			if err == unix.EINTR && count == 0 {
				continue
			}
			// Get failed because we called w.Close()
			if err == unix.EBADF && w.isClosed() {
				return
			}
			// There was an error not caused by calling w.Close()
			if !w.sendError(err) {
				return
			}
		}

		p := pevents[:count]
		for _, pevent := range p {
			if pevent.Source != unix.PORT_SOURCE_FILE {
				// Event from unexpected source received; should never happen.
				if !w.sendError(errors.New("Event from unexpected source received")) {
					return
				}
				continue
			}

			err = w.handleEvent(&pevent)
			if err != nil {
				if !w.sendError(err) {
					return
				}
			}
		}
	}
}

func (w *Watcher) handleDirectory(path string, stat os.FileInfo, handler func(string, os.FileInfo) error) error {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}

	// Handle all children of the directory.
	for _, finfo := range files {
		if !finfo.IsDir() {
			err := handler(filepath.Join(path, finfo.Name()), finfo)
			if err != nil {
				return err
			}
		}
	}

	// And finally handle the directory itself.
	return handler(path, stat)
}

// handleEvent might need to emit more than one fsnotify event
// if the events bitmap matches more than one event type
// (e.g. the file was both modified and had the
// attributes changed between when the association
// was created and the when event was returned)
func (w *Watcher) handleEvent(event *unix.PortEvent) error {
	events := event.Events
	path := event.Path
	fmode := event.Cookie.(os.FileMode)

	var toSend *Event
	reRegister := true

	w.mu.Lock()
	_, watchedDir := w.dirs[path]
	w.mu.Unlock()

	if events&unix.FILE_DELETE == unix.FILE_DELETE {
		toSend = &Event{path, Remove}
		if !w.sendEvent(*toSend) {
			return nil
		}
		reRegister = false
	}
	if events&unix.FILE_RENAME_FROM == unix.FILE_RENAME_FROM {
		toSend = &Event{path, Rename}
		if !w.sendEvent(*toSend) {
			return nil
		}
		// Don't keep watching the new file name
		reRegister = false
	}
	if events&unix.FILE_RENAME_TO == unix.FILE_RENAME_TO {
		// We don't report a Rename event for this case, because
		// Rename events are interpreted as referring to the _old_ name
		// of the file, and in this case the event would refer to the
		// new name of the file. This type of rename event is not
		// supported by fsnotify.

		// inotify reports a Remove event in this case, so we simulate
		// this here.
		toSend = &Event{path, Remove}
		if !w.sendEvent(*toSend) {
			return nil
		}
		// Don't keep watching the file that was removed
		reRegister = false
	}

	// The file is gone, nothing left to do.
	if !reRegister {
		if watchedDir {
			w.mu.Lock()
			delete(w.dirs, path)
			w.mu.Unlock()
		}
		return nil
	}

	// If we didn't get a deletion the file still exists and we're going to have to watch it again.
	// Let's Stat it now so that we can compare permissions and have what we need
	// to continue watching the file

	stat, err := os.Stat(path)
	if err != nil {
		return err
	}

	if events&unix.FILE_MODIFIED == unix.FILE_MODIFIED {
		if fmode.IsDir() {
			if watchedDir {
				if err := w.updateDirectory(path); err != nil {
					return err
				}
			}
		} else {
			toSend = &Event{path, Write}
			if !w.sendEvent(*toSend) {
				return nil
			}
		}
	}
	if events&unix.FILE_ATTRIB == unix.FILE_ATTRIB {
		// Only send Chmod if perms changed
		if stat.Mode().Perm() != fmode.Perm() {
			toSend = &Event{path, Chmod}
			if !w.sendEvent(*toSend) {
				return nil
			}
		}
	}

	// If we get here, it means we've hit an event above that requires us to
	// continue watching the file or directory
	return w.associateFile(path, stat)
}

func (w *Watcher) updateDirectory(path string) error {
	// The directory was modified, so we must find unwatched entities and
	// watch them. If something was removed from the directory, nothing will
	// happen, as everything else should still be watched.
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}

	for _, finfo := range files {
		path := filepath.Join(path, finfo.Name())
		if w.port.PathIsWatched(path) {
			continue
		}

		err := w.associateFile(path, finfo)
		if err != nil {
			if !w.sendError(err) {
				return nil
			}
		}
		if !w.sendEvent(Event{path, Create}) {
			return nil
		}
	}
	return nil
}

func (w *Watcher) associateFile(path string, stat os.FileInfo) error {
	if w.isClosed() {
		return errors.New("FEN watcher already closed")
	}
	// This is primarily protecting the call to AssociatePath
	// but it is important and intentional that the call to
	// PathIsWatched is also protected by this mutex.
	// Without this mutex, AssociatePath has been seen
	// to error out that the path is already associated.
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.port.PathIsWatched(path) {
		// Remove the old association in favor of this one
		// If we get ENOENT, then while the x/sys/unix wrapper
		// still thought that this path was associated,
		// the underlying event port did not. This call will
		// have cleared up that discrepancy. The most likely
		// cause is that the event has fired but we haven't
		// processed it yet.
		if err := w.port.DissociatePath(path); err != nil && err != unix.ENOENT {
			return err
		}
	}
	// FILE_NOFOLLOW means we watch symlinks themselves rather than their targets
	return w.port.AssociatePath(path, stat,
		unix.FILE_MODIFIED|unix.FILE_ATTRIB|unix.FILE_NOFOLLOW,
		stat.Mode())
}

func (w *Watcher) dissociateFile(path string, stat os.FileInfo) error {
	if !w.port.PathIsWatched(path) {
		return nil
	}
	return w.port.DissociatePath(path)
}
