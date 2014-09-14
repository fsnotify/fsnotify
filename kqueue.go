// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd openbsd netbsd dragonfly darwin

package fsnotify

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error

	kq              int               // File descriptor (as returned by the kqueue() syscall).
	watches         map[string]int    // Map of watched file descriptors (key: path).
	externalWatches map[string]bool   // Map of watches added by user of the library.
	dirFlags        map[string]uint32 // Map of watched directories to fflags used in kqueue.
	paths           map[int]pathInfo  // Map file descriptors to path names for processing kqueue events.
	fileExists      map[string]bool   // Keep track of if we know this file exists (to stop duplicate create events).
	done            chan bool         // Channel for sending a "quit message" to the reader goroutine
	isClosed        bool              // Set to true when Close() is first called

	mu sync.Mutex // Mutex for the Watcher itself (isClosed).

	wmut  sync.Mutex // Protects access to watches.
	pmut  sync.Mutex // Protects access to paths.
	ewmut sync.Mutex // Protects access to externalWatches.

	dirmut sync.Mutex // Protects access to dirFlags.
	femut  sync.Mutex // Protects access to fileExists.
}

type pathInfo struct {
	name  string
	isDir bool
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	kq, err := kqueue()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		kq:              kq,
		watches:         make(map[string]int),
		dirFlags:        make(map[string]uint32),
		paths:           make(map[int]pathInfo),
		fileExists:      make(map[string]bool),
		externalWatches: make(map[string]bool),
		Events:          make(chan Event),
		Errors:          make(chan error),
		done:            make(chan bool),
	}

	go w.readEvents()
	return w, nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.isClosed {
		w.mu.Unlock()
		return nil
	}
	w.isClosed = true
	w.mu.Unlock()

	// Send "quit" message to the reader goroutine:
	w.done <- true
	w.wmut.Lock()
	ws := w.watches
	w.wmut.Unlock()
	for name := range ws {
		w.Remove(name)
	}

	return nil
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	w.ewmut.Lock()
	w.externalWatches[name] = true
	w.ewmut.Unlock()
	return w.addWatch(name, noteAllEvents)
}

// Remove stops watching the the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	name = filepath.Clean(name)
	w.wmut.Lock()
	watchfd, ok := w.watches[name]
	w.wmut.Unlock()
	if !ok {
		return fmt.Errorf("can't remove non-existent kevent watch for: %s", name)
	}

	const registerRemove = syscall.EV_DELETE
	if err := register(w.kq, []int{watchfd}, registerRemove, 0); err != nil {
		return err
	}

	syscall.Close(watchfd)

	w.wmut.Lock()
	delete(w.watches, name)
	w.wmut.Unlock()
	w.dirmut.Lock()
	delete(w.dirFlags, name)
	w.dirmut.Unlock()
	w.pmut.Lock()
	isDir := w.paths[watchfd].isDir
	delete(w.paths, watchfd)
	w.pmut.Unlock()

	// Find all watched paths that are in this directory that are not external.
	if isDir {
		var pathsToRemove []string
		w.pmut.Lock()
		for _, path := range w.paths {
			wdir, _ := filepath.Split(path.name)
			if filepath.Clean(wdir) == filepath.Clean(name) {
				w.ewmut.Lock()
				if !w.externalWatches[path.name] {
					pathsToRemove = append(pathsToRemove, path.name)
				}
				w.ewmut.Unlock()
			}
		}
		w.pmut.Unlock()
		for _, name := range pathsToRemove {
			// Since these are internal, not much sense in propagating error
			// to the user, as that will just confuse them with an error about
			// a path they did not explicitly watch themselves.
			w.Remove(name)
		}
	}

	return nil
}

// Watch all events (except NOTE_EXTEND, NOTE_LINK, NOTE_REVOKE)
const noteAllEvents = syscall.NOTE_DELETE | syscall.NOTE_WRITE | syscall.NOTE_ATTRIB | syscall.NOTE_RENAME

// keventWaitTime to block on each read from kevent
var keventWaitTime = durationToTimespec(100 * time.Millisecond)

// addWatch adds name to the watched file set.
// The flags are interpreted as described in kevent(2).
func (w *Watcher) addWatch(name string, flags uint32) error {
	w.mu.Lock()
	if w.isClosed {
		w.mu.Unlock()
		return errors.New("kevent instance already closed")
	}
	w.mu.Unlock()

	// Make ./name and name equivalent
	name = filepath.Clean(name)

	w.wmut.Lock()
	watchfd, alreadyWatching := w.watches[name]
	w.wmut.Unlock()

	var isDir bool

	if alreadyWatching {
		// We already have a watch, but we can still override flags
		w.pmut.Lock()
		isDir = w.paths[watchfd].isDir
		w.pmut.Unlock()
	} else {
		fi, err := os.Lstat(name)
		if err != nil {
			return err
		}

		// don't watch socket
		if fi.Mode()&os.ModeSocket == os.ModeSocket {
			return nil
		}

		// Follow Symlinks
		// Unfortunately, Linux can add bogus symlinks to watch list without
		// issue, and Windows can't do symlinks period (AFAIK). To  maintain
		// consistency, we will act like everything is fine. There will simply
		// be no file events for broken symlinks.
		// Hence the returns of nil on errors.
		if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
			name, err = filepath.EvalSymlinks(name)
			if err != nil {
				return nil
			}

			fi, err = os.Lstat(name)
			if err != nil {
				return nil
			}
		}

		watchfd, err = syscall.Open(name, openMode, 0700)
		if watchfd == -1 {
			return os.NewSyscallError("Open", err)
		}

		isDir = fi.IsDir()
	}

	const registerAdd = syscall.EV_ADD | syscall.EV_CLEAR | syscall.EV_ENABLE
	if err := register(w.kq, []int{watchfd}, registerAdd, noteAllEvents); err != nil {
		syscall.Close(watchfd)
		return err
	}

	if !alreadyWatching {
		w.wmut.Lock()
		w.watches[name] = watchfd
		w.wmut.Unlock()

		w.pmut.Lock()
		w.paths[watchfd] = pathInfo{name: name, isDir: isDir}
		w.pmut.Unlock()
	}

	if isDir {
		// Watch the directory if it has not been watched before,
		// or if it was watched before, but perhaps only a NOTE_DELETE (watchDirectoryFiles)
		w.dirmut.Lock()
		watchDir := (flags&syscall.NOTE_WRITE) == syscall.NOTE_WRITE &&
			(!alreadyWatching || (w.dirFlags[name]&syscall.NOTE_WRITE) != syscall.NOTE_WRITE)
		// Store flags so this watch can be updated later
		w.dirFlags[name] = flags
		w.dirmut.Unlock()

		if watchDir {
			if err := w.watchDirectoryFiles(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// readEvents reads from the kqueue file descriptor, converts the
// received events into Event objects and sends them via the Events channel
func (w *Watcher) readEvents() {
	eventBuffer := make([]syscall.Kevent_t, 10)

	for {
		// See if there is a message on the "done" channel
		select {
		case <-w.done:
			errno := syscall.Close(w.kq)
			if errno != nil {
				w.Errors <- os.NewSyscallError("close", errno)
			}
			close(w.Events)
			close(w.Errors)
			return
		default:
		}

		// Get new events
		kevents, err := read(w.kq, eventBuffer, &keventWaitTime)
		// EINTR is okay, the syscall was interrupted before timeout expired.
		if err != nil && err != syscall.EINTR {
			w.Errors <- os.NewSyscallError("Kevent", err)
			continue
		}

		// Flush the events we received to the Events channel
		for len(kevents) > 0 {
			watchEvent := &kevents[0]
			watchfd := int(watchEvent.Ident)
			mask := uint32(watchEvent.Fflags)

			w.pmut.Lock()
			path := w.paths[watchfd]
			w.pmut.Unlock()

			event := newEvent(path.name, mask)

			if path.isDir && !(event.Op&Remove == Remove) {
				// Double check to make sure the directory exist. This can happen when
				// we do a rm -fr on a recursively watched folders and we receive a
				// modification event first but the folder has been deleted and later
				// receive the delete event
				if _, err := os.Lstat(event.Name); os.IsNotExist(err) {
					// mark is as delete event
					event.Op |= Remove
				}
			}

			if path.isDir && event.Op&Write == Write && !(event.Op&Remove == Remove) {
				w.sendDirectoryChangeEvents(event.Name)
			} else {
				// Send the event on the Events channel
				w.Events <- event
			}

			// Move to next event
			kevents = kevents[1:]

			if event.Op&Rename == Rename {
				w.Remove(event.Name)
				w.femut.Lock()
				delete(w.fileExists, event.Name)
				w.femut.Unlock()
			}
			if event.Op&Remove == Remove {
				w.Remove(event.Name)
				w.femut.Lock()
				delete(w.fileExists, event.Name)
				w.femut.Unlock()

				// Look for a file that may have overwritten this.
				// For example, mv f1 f2 will delete f2, then create f2.
				fileDir, _ := filepath.Split(event.Name)
				fileDir = filepath.Clean(fileDir)
				w.wmut.Lock()
				_, found := w.watches[fileDir]
				w.wmut.Unlock()
				if found {
					// make sure the directory exists before we watch for changes. When we
					// do a recursive watch and perform rm -fr, the parent directory might
					// have gone missing, ignore the missing directory and let the
					// upcoming delete event remove the watch from the parent directory.
					if _, err := os.Lstat(fileDir); os.IsExist(err) {
						w.sendDirectoryChangeEvents(fileDir)
						// FIXME: should this be for events on files or just isDir?
					}
				}
			}
		}
	}
}

// newEvent returns an platform-independent Event based on kqueue Fflags.
func newEvent(name string, mask uint32) Event {
	e := Event{Name: name}
	if mask&syscall.NOTE_DELETE == syscall.NOTE_DELETE {
		e.Op |= Remove
	}
	if mask&syscall.NOTE_WRITE == syscall.NOTE_WRITE {
		e.Op |= Write
	}
	if mask&syscall.NOTE_RENAME == syscall.NOTE_RENAME {
		e.Op |= Rename
	}
	if mask&syscall.NOTE_ATTRIB == syscall.NOTE_ATTRIB {
		e.Op |= Chmod
	}
	return e
}

func newCreateEvent(name string) Event {
	return Event{Name: name, Op: Create}
}

// watchDirectoryFiles to mimic inotify when adding a watch on a directory
func (w *Watcher) watchDirectoryFiles(dirPath string) error {
	// Get all files
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return err
	}

	for _, fileInfo := range files {
		filePath := filepath.Join(dirPath, fileInfo.Name())
		if err := w.internalWatch(filePath, fileInfo); err != nil {
			return err
		}

		w.femut.Lock()
		w.fileExists[filePath] = true
		w.femut.Unlock()
	}

	return nil
}

// sendDirectoryEvents searches the directory for newly created files
// and sends them over the event channel. This functionality is to have
// the BSD version of fsnotify match Linux inotify which provides a
// create event for files created in a watched directory.
func (w *Watcher) sendDirectoryChangeEvents(dirPath string) {
	// Get all files
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		w.Errors <- err
	}

	// Search for new files
	for _, fileInfo := range files {
		filePath := filepath.Join(dirPath, fileInfo.Name())
		w.femut.Lock()
		_, doesExist := w.fileExists[filePath]
		w.femut.Unlock()
		if !doesExist {
			// Send create event
			w.Events <- newCreateEvent(filePath)
		}

		// like watchDirectoryFiles (but without doing another ReadDir)
		if err := w.internalWatch(filePath, fileInfo); err != nil {
			return
		}

		w.femut.Lock()
		w.fileExists[filePath] = true
		w.femut.Unlock()
	}
}

func (w *Watcher) internalWatch(name string, fileInfo os.FileInfo) error {
	if fileInfo.IsDir() {
		// mimic Linux providing delete events for subdirectories
		// but preserve the flags used if currently watching subdirectory
		w.dirmut.Lock()
		flags := w.dirFlags[name]
		w.dirmut.Unlock()

		flags |= syscall.NOTE_DELETE
		return w.addWatch(name, flags)
	}

	// watch file to mimic Linux inotify
	return w.addWatch(name, noteAllEvents)
}

// kqueue creates a new kernel event queue and returns a descriptor.
func kqueue() (kq int, err error) {
	kq, err = syscall.Kqueue()
	if kq == -1 {
		return kq, os.NewSyscallError("Kqueue", err)
	}
	return kq, nil
}

// register events with the queue
func register(kq int, fds []int, flags int, fflags uint32) error {
	changes := make([]syscall.Kevent_t, len(fds))

	for i, fd := range fds {
		// SetKevent converts int to the platform-specific types:
		syscall.SetKevent(&changes[i], fd, syscall.EVFILT_VNODE, flags)
		changes[i].Fflags = fflags
	}

	// register the events
	success, err := syscall.Kevent(kq, changes, nil, nil)
	if success == -1 {
		return os.NewSyscallError("Kevent", err)
	}
	return nil
}

// read retrieves pending events
// A timeout of nil blocks indefinitely, while 0 polls the queue.
func read(kq int, events []syscall.Kevent_t, timeout *syscall.Timespec) ([]syscall.Kevent_t, error) {
	n, err := syscall.Kevent(kq, nil, events, timeout)
	if err != nil {
		return nil, err
	}
	return events[0:n], nil
}

// durationToTimespec prepares a timeout value
func durationToTimespec(d time.Duration) syscall.Timespec {
	return syscall.NsecToTimespec(d.Nanoseconds())
}
