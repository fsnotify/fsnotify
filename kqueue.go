// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build freebsd || openbsd || netbsd || dragonfly || darwin
// +build freebsd openbsd netbsd dragonfly darwin

package fsnotify

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error
	done   chan struct{} // Channel for sending a "quit message" to the reader goroutine

	kq int // File descriptor (as returned by the kqueue() syscall).

	mu              sync.Mutex        // Used for consistent state updates and access
	muInternal      sync.Mutex        // Used for private access to state
	watches         map[string]int    // Map of watched file descriptors (key: path).
	externalWatches map[string]bool   // Map of watches added by user of the library.
	dirFlags        map[string]uint32 // Map of watched directories to fflags used in kqueue.
	paths           map[int]pathInfo  // Map file descriptors to path names for processing kqueue events.
	fileExists      map[string]bool   // Keep track of if we know this file exists (to stop duplicate create events).
	isClosed        bool              // Set to true when Close() is first called
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
		done:            make(chan struct{}),
	}

	go w.readEvents()
	return w, nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed {
		return nil
	}

	w.isClosed = true

	// copy paths to remove while locked
	var pathsToRemove = make([]string, 0, len(w.watches))
	for name := range w.watches {
		pathsToRemove = append(pathsToRemove, name)
	}

	for _, name := range pathsToRemove {
		w.remove(name)
	}

	// send a "quit" message to the reader goroutine
	close(w.done)

	return nil
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed {
		return ErrWatcherClosed
	}

	w.externalWatches[name] = true
	_, err := w.addWatch(name, noteAllEvents)
	return err
}

// Remove stops watching the the named file or directory recursively.
func (w *Watcher) Remove(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed {
		return ErrWatcherClosed
	}

	return w.remove(name)
}

func (w *Watcher) remove(name string) error {

	pathsToRemove := []string{name}

	for len(pathsToRemove) != 0 {
		name = filepath.Clean(pathsToRemove[0])
		pathsToRemove = pathsToRemove[1:]

		if err := func() error {
			w.muInternal.Lock()
			defer w.muInternal.Unlock()
			watchfd, ok := w.watches[name]
			if !ok {
				return fmt.Errorf("can't remove watch: %w: %s", ErrWatchDoesNotExist, name)
			}

			const registerRemove = unix.EV_DELETE
			if err := register(w.kq, []int{watchfd}, registerRemove, 0); err != nil {
				return fmt.Errorf("can't remove watch: %w: %s", err, name)
			}

			unix.Close(watchfd)

			isDir := w.paths[watchfd].isDir
			delete(w.watches, name)
			delete(w.paths, watchfd)
			delete(w.dirFlags, name)

			// Find all watched paths that are in this directory that are not external.
			if isDir {
				dirname := name
				for _, path := range w.paths {
					wdir, _ := filepath.Split(path.name)
					if filepath.Clean(wdir) == dirname {
						if !w.externalWatches[path.name] {
							pathsToRemove = append(pathsToRemove, path.name)
						}
					}
				}
			}
			return nil
		}(); err != nil {
			return err
		}
	}

	return nil
}

// Watch all events (except NOTE_EXTEND, NOTE_LINK, NOTE_REVOKE)
const noteAllEvents = unix.NOTE_DELETE | unix.NOTE_WRITE | unix.NOTE_ATTRIB | unix.NOTE_RENAME

// keventWaitTime to block on each read from kevent
var keventWaitTime = durationToTimespec(100 * time.Millisecond)

// addWatch adds name to the watched file set.
// The flags are interpreted as described in kevent(2).
// Returns the real path to the file which was added, if any, which may be different from the one passed in the case of symlinks.
func (w *Watcher) addWatch(name string, flags uint32) (string, error) {
	w.muInternal.Lock()
	defer w.muInternal.Unlock()
	var isDir bool
	// Make ./name and name equivalent
	name = filepath.Clean(name)

	if w.isClosed {
		return "", errors.New("kevent instance already closed")
	}
	watchfd, alreadyWatching := w.watches[name]
	// We already have a watch, but we can still override flags.
	if alreadyWatching {
		isDir = w.paths[watchfd].isDir
	}

	if !alreadyWatching {
		fi, err := os.Lstat(name)
		if err != nil {
			return "", err
		}

		// Don't watch sockets.
		if fi.Mode()&os.ModeSocket == os.ModeSocket {
			return "", nil
		}

		// Don't watch named pipes.
		if fi.Mode()&os.ModeNamedPipe == os.ModeNamedPipe {
			return "", nil
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
				return "", nil
			}

			_, alreadyWatching = w.watches[name]

			if alreadyWatching {
				return name, nil
			}

			fi, err = os.Lstat(name)
			if err != nil {
				return "", nil
			}
		}

		watchfd, err = unix.Open(name, openMode, 0700)
		if watchfd == -1 {
			return "", err
		}

		isDir = fi.IsDir()
	}

	const registerAdd = unix.EV_ADD | unix.EV_CLEAR | unix.EV_ENABLE
	if err := register(w.kq, []int{watchfd}, registerAdd, flags); err != nil {
		unix.Close(watchfd)
		return "", err
	}

	if !alreadyWatching {
		w.watches[name] = watchfd
		w.paths[watchfd] = pathInfo{name: name, isDir: isDir}
	}

	if isDir {
		// Watch the directory if it has not been watched before,
		// or if it was watched before, but perhaps only a NOTE_DELETE (watchDirectoryFiles)

		watchDir := (flags&unix.NOTE_WRITE) == unix.NOTE_WRITE &&
			(!alreadyWatching || (w.dirFlags[name]&unix.NOTE_WRITE) != unix.NOTE_WRITE)
		// Store flags so this watch can be updated later
		w.dirFlags[name] = flags

		if watchDir {
			if err := w.watchDirectoryFiles(name); err != nil {
				return "", err
			}
		}
	}
	return name, nil
}

// readEvents reads from kqueue and converts the received kevents into
// Event values that it sends down the Events channel.
func (w *Watcher) readEvents() {
	eventBuffer := make([]unix.Kevent_t, 10)

loop:
	for {
		// See if there is a message on the "done" channel
		select {
		case <-w.done:
			break loop
		default:
		}

		// Get new events
		kevents, err := read(w.kq, eventBuffer, &keventWaitTime)
		// EINTR is okay, the syscall was interrupted before timeout expired.
		if err != nil && err != unix.EINTR {
			select {
			case w.Errors <- err:
			case <-w.done:
				break loop
			}
			continue
		}

		// Flush the events we received to the Events channel
		for len(kevents) > 0 {
			kevent := &kevents[0]
			watchfd := int(kevent.Ident)
			mask := uint32(kevent.Fflags)
			if done := func() bool {
				w.muInternal.Lock()
				defer w.muInternal.Unlock()
				path := w.paths[watchfd]
				event := newEvent(path.name, mask)

				if path.isDir && !(event.Op&Remove == Remove) {
					// Double check to make sure the directory exists. This can happen when
					// we do a rm -fr on a recursively watched folders and we receive a
					// modification event first but the folder has been deleted and later
					// receive the delete event
					if _, err := os.Lstat(event.Name); os.IsNotExist(err) {
						// mark is as delete event
						event.Op |= Remove
					}
				}

				if event.Op&Rename == Rename || event.Op&Remove == Remove {
					w.Remove(event.Name)
					delete(w.fileExists, event.Name)
				}

				if path.isDir && event.Op&Write == Write && !(event.Op&Remove == Remove) {
					w.sendDirectoryChangeEvents(event.Name)
				} else {
					// Send the event on the Events channel.
					select {
					case w.Events <- event:
					case <-w.done:
						return true
					}
				}

				if event.Op&Remove == Remove {
					// Look for a file that may have overwritten this.
					// For example, mv f1 f2 will delete f2, then create f2.
					if path.isDir {
						fileDir := filepath.Clean(event.Name)
						_, found := w.watches[fileDir]
						if found {
							// make sure the directory exists before we watch for changes. When we
							// do a recursive watch and perform rm -fr, the parent directory might
							// have gone missing, ignore the missing directory and let the
							// upcoming delete event remove the watch from the parent directory.
							if _, err := os.Lstat(fileDir); err == nil {
								w.sendDirectoryChangeEvents(fileDir)
							}
						}
					} else {
						filePath := filepath.Clean(event.Name)
						if fileInfo, err := os.Lstat(filePath); err == nil {
							w.sendFileCreatedEventIfNew(filePath, fileInfo)
						}
					}
				}
				return false
			}(); done {
				break loop
			}

			// Move to next event
			kevents = kevents[1:]
		}
	}

	// cleanup
	err := unix.Close(w.kq)
	if err != nil {
		// only way the previous loop breaks is if w.done was closed so we need to async send to w.Errors.
		select {
		case w.Errors <- err:
		default:
		}
	}
	close(w.Events)
	close(w.Errors)
}

// newEvent returns an platform-independent Event based on kqueue Fflags.
func newEvent(name string, mask uint32) Event {
	e := Event{Name: name}
	if mask&unix.NOTE_DELETE == unix.NOTE_DELETE {
		e.Op |= Remove
	}
	if mask&unix.NOTE_WRITE == unix.NOTE_WRITE {
		e.Op |= Write
	}
	if mask&unix.NOTE_RENAME == unix.NOTE_RENAME {
		e.Op |= Rename
	}
	if mask&unix.NOTE_ATTRIB == unix.NOTE_ATTRIB {
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
		filePath, err = w.internalWatch(filePath, fileInfo)
		if err != nil {
			return err
		}

		w.fileExists[filePath] = true
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
		select {
		case w.Errors <- err:
		case <-w.done:
			return
		}
	}

	// Search for new files
	for _, fileInfo := range files {
		filePath := filepath.Join(dirPath, fileInfo.Name())
		err := w.sendFileCreatedEventIfNew(filePath, fileInfo)

		if err != nil {
			return
		}
	}
}

// sendFileCreatedEvent sends a create event if the file isn't already being tracked.
func (w *Watcher) sendFileCreatedEventIfNew(filePath string, fileInfo os.FileInfo) (err error) {
	_, doesExist := w.fileExists[filePath]
	if !doesExist {
		// Send create event
		select {
		case w.Events <- newCreateEvent(filePath):
		case <-w.done:
			return
		}
	}

	// like watchDirectoryFiles (but without doing another ReadDir)
	filePath, err = w.internalWatch(filePath, fileInfo)
	if err != nil {
		return err
	}

	w.fileExists[filePath] = true

	return nil
}

func (w *Watcher) internalWatch(name string, fileInfo os.FileInfo) (string, error) {
	if fileInfo.IsDir() {
		// mimic Linux providing delete events for subdirectories
		// but preserve the flags used if currently watching subdirectory
		flags := w.dirFlags[name]

		flags |= unix.NOTE_DELETE | unix.NOTE_RENAME
		return w.addWatch(name, flags)
	}

	// watch file to mimic Linux inotify
	return w.addWatch(name, noteAllEvents)
}

// kqueue creates a new kernel event queue and returns a descriptor.
func kqueue() (kq int, err error) {
	kq, err = unix.Kqueue()
	if kq == -1 {
		return kq, err
	}
	return kq, nil
}

// register events with the queue
func register(kq int, fds []int, flags int, fflags uint32) error {
	changes := make([]unix.Kevent_t, len(fds))

	for i, fd := range fds {
		// SetKevent converts int to the platform-specific types:
		unix.SetKevent(&changes[i], fd, unix.EVFILT_VNODE, flags)
		changes[i].Fflags = fflags
	}

	// register the events
	success, err := unix.Kevent(kq, changes, nil, nil)
	if success == -1 {
		return err
	}
	return nil
}

// read retrieves pending events, or waits until an event occurs.
// A timeout of nil blocks indefinitely, while 0 polls the queue.
func read(kq int, events []unix.Kevent_t, timeout *unix.Timespec) ([]unix.Kevent_t, error) {
	n, err := unix.Kevent(kq, nil, events, timeout)
	if err != nil {
		return nil, err
	}
	return events[0:n], nil
}

// durationToTimespec prepares a timeout value
func durationToTimespec(d time.Duration) unix.Timespec {
	return unix.NsecToTimespec(d.Nanoseconds())
}
