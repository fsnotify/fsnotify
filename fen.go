// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build solaris
// +build solaris

package fsnotify

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error

	port *unix.EventPort

	done     chan struct{} // Channel for sending a "quit message" to the reader goroutine
	doneResp chan struct{} // Channel to respond to Close
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	var err error

	w := new(Watcher)
	w.Events = make(chan Event)
	w.Errors = make(chan error)
	w.port, err = unix.NewEventPort()
	if err != nil {
		return nil, err
	}
	w.done = make(chan struct{})
	w.doneResp = make(chan struct{})

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
	if w.isClosed() {
		return nil
	}
	close(w.done)
	w.port.Close()
	<-w.doneResp
	return nil
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
		return w.handleDirectory(name, stat, w.dissociateFile)
	default:
		return w.port.DissociatePath(name)
	}
}

// readEvents contains the main loop that runs in a goroutine watching for events.
func (w *Watcher) readEvents() {
	// If this function returns, the watcher has been closed and we can
	// close these channels
	defer close(w.doneResp)
	defer close(w.Errors)
	defer close(w.Events)

	pevents := make([]unix.PortEvent, 8, 8)
	for {
		count, err := w.port.Get(pevents, 1, nil)
		if err != nil {
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

func (w *Watcher) handleEvent(event *unix.PortEvent) error {
	events := event.Events
	path := event.Path
	fmode := event.Cookie.(os.FileMode)

	var toSend *Event
	reRegister := true

	switch {
	case events&unix.FILE_MODIFIED == unix.FILE_MODIFIED:
		if fmode.IsDir() {
			if err := w.updateDirectory(path); err != nil {
				return err
			}
		} else {
			toSend = &Event{path, Write}
		}
	case events&unix.FILE_ATTRIB == unix.FILE_ATTRIB:
		toSend = &Event{path, Chmod}
	case events&unix.FILE_DELETE == unix.FILE_DELETE:
		toSend = &Event{path, Remove}
		reRegister = false
	case events&unix.FILE_RENAME_FROM == unix.FILE_RENAME_FROM:
		toSend = &Event{path, Rename}
		// Don't keep watching the new file name
		reRegister = false
	case events&unix.FILE_RENAME_TO == unix.FILE_RENAME_TO:
		// We don't report a Rename event for this case, because
		// Rename events are interpreted as referring to the _old_ name
		// of the file, and in this case the event would refer to the
		// new name of the file. This type of rename event is not
		// supported by fsnotify.

		// inotify reports a Remove event in this case, so we simulate
		// this here.
		toSend = &Event{path, Remove}
		// Don't keep watching the file that was removed
		reRegister = false
	default:
		return errors.New("unknown event received")
	}

	if toSend != nil {
		if !w.sendEvent(*toSend) {
			return nil
		}
	}
	if !reRegister {
		return nil
	}

	// If we get here, it means we've hit an event above that requires us to
	// continue watching the file or directory
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	return w.associateFile(path, stat)
}

func (w *Watcher) updateDirectory(path string) error {
	// The directory was modified, so we must find unwatched entites and
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
	mode := unix.FILE_MODIFIED | unix.FILE_ATTRIB | unix.FILE_NOFOLLOW
	fmode := stat.Mode()
	return w.port.AssociatePath(path, stat, mode, fmode)
}

func (w *Watcher) dissociateFile(path string, stat os.FileInfo) error {
	if !w.port.PathIsWatched(path) {
		return nil
	}
	return w.port.DissociatePath(path)
}
