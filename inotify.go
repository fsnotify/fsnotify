// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events  chan Event
	Errors  chan error
	mu      sync.Mutex        // Map access
	fd      int               // File descriptor (as returned by the inotify_init() syscall)
	watches map[string]*watch // Map of inotify watches (key: path)
	paths   map[int]string    // Map of watched paths (key: watch descriptor)
	done    chan struct{}     // Channel for sending a "quit message" to the reader goroutine
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	fd, errno := syscall.InotifyInit()
	if fd == -1 {
		return nil, os.NewSyscallError("inotify_init", errno)
	}
	w := &Watcher{
		fd:      fd,
		watches: make(map[string]*watch),
		paths:   make(map[int]string),
		Events:  make(chan Event),
		Errors:  make(chan error),
		done:    make(chan struct{}),
	}

	go w.readEvents()
	return w, nil
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

	// Send 'close' signal to goroutine, and set the Watcher to closed.
	close(w.done)

	// Remove all watches.
	// Everything after this may generate errors because the inotify channel
	// has been closed; we don't care.
	numWatches := w.removeAll()

	// If no watches were removed, it's possible syscall.Read is still blocking.
	// In this case, create a watch and remove it to wake it up.
	// If that fails, there's really nothing left to do. we've done our best,
	// but the goroutine may be alive forever.
	if numWatches == 0 {
		wd, _ := syscall.InotifyAddWatch(w.fd, ".", syscall.IN_DELETE_SELF)
		syscall.InotifyRmWatch(w.fd, uint32(wd))
	}

	return nil
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	name = filepath.Clean(name)
	if w.isClosed() {
		return errors.New("inotify instance already closed")
	}

	const agnosticEvents = syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM |
		syscall.IN_CREATE | syscall.IN_ATTRIB | syscall.IN_MODIFY |
		syscall.IN_MOVE_SELF | syscall.IN_DELETE | syscall.IN_DELETE_SELF

	var flags uint32 = agnosticEvents

	w.mu.Lock()
	watchEntry, found := w.watches[name]
	w.mu.Unlock()
	if found {
		watchEntry.flags |= flags
		flags |= syscall.IN_MASK_ADD
	}
	wd, errno := syscall.InotifyAddWatch(w.fd, name, flags)
	if wd == -1 {
		return os.NewSyscallError("inotify_add_watch", errno)
	}

	w.mu.Lock()
	w.watches[name] = &watch{wd: uint32(wd), flags: flags}
	w.paths[wd] = name
	w.mu.Unlock()

	return nil
}

// Remove stops watching the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	name = filepath.Clean(name)

	// Fetch the watch.
	w.mu.Lock()
	defer w.mu.Unlock()
	watch, ok := w.watches[name]

	// Remove it from inotify.
	if !ok {
		return fmt.Errorf("can't remove non-existent inotify watch for: %s", name)
	}
	success, errno := syscall.InotifyRmWatch(w.fd, watch.wd)
	if success == -1 {
		return os.NewSyscallError("inotify_rm_watch", errno)
	}
	delete(w.watches, name)
	return nil
}

// removeAll watches
func (w *Watcher) removeAll() int {
	removed := 0
	w.mu.Lock()
	defer w.mu.Unlock()
	for name, watch := range w.watches {
		success, _ := syscall.InotifyRmWatch(w.fd, watch.wd)
		if success != -1 {
			removed++
		}
		delete(w.watches, name)
	}
	return removed
}

type watch struct {
	wd    uint32 // Watch descriptor (as returned by the inotify_add_watch() syscall)
	flags uint32 // inotify flags of this watch (see inotify(7) for the list of valid flags)
}

// readEvents reads from the inotify file descriptor, converts the
// received events into Event objects and sends them via the Events channel
func (w *Watcher) readEvents() {
	var (
		buf   [syscall.SizeofInotifyEvent * 4096]byte // Buffer for a maximum of 4096 raw events
		n     int                                     // Number of bytes read with read()
		errno error                                   // Syscall errno
	)

	defer close(w.Errors)
	defer close(w.Events)
	defer syscall.Close(w.fd)

	for {
		// See if we have been closed.
		if w.isClosed() {
			return
		}

		n, errno = syscall.Read(w.fd, buf[:])
		// If a signal interrupted execution, see if we've been asked to close, and try again.
		// http://man7.org/linux/man-pages/man7/signal.7.html :
		// "Before Linux 3.8, reads from an inotify(7) file descriptor were not restartable"
		if errno == syscall.EINTR {
			continue
		}

		// syscall.Read might have been woken up by Close. If so, we're done.
		if w.isClosed() {
			return
		}

		// If EOF is received
		if n == 0 {
			close(w.done)
			return
		}

		if n < 0 {
			select {
			case w.Errors <- os.NewSyscallError("read", errno):
			case <-w.done:
				return
			}
			continue
		}
		if n < syscall.SizeofInotifyEvent {
			select {
			case w.Errors <- errors.New("inotify: short read in readEvents()"):
			case <-w.done:
				return
			}
			continue
		}

		var offset uint32
		// We don't know how many events we just read into the buffer
		// While the offset points to at least one whole event...
		for offset <= uint32(n-syscall.SizeofInotifyEvent) {
			// Point "raw" to the event in the buffer
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))

			mask := uint32(raw.Mask)
			nameLen := uint32(raw.Len)
			// If the event happened to the watched directory or the watched file, the kernel
			// doesn't append the filename to the event, but we would like to always fill the
			// the "Name" field with a valid filename. We retrieve the path of the watch from
			// the "paths" map.
			w.mu.Lock()
			name := w.paths[int(raw.Wd)]
			w.mu.Unlock()
			if nameLen > 0 {
				// Point "bytes" at the first byte of the filename
				bytes := (*[syscall.PathMax]byte)(unsafe.Pointer(&buf[offset+syscall.SizeofInotifyEvent]))
				// The filename is padded with NULL bytes. TrimRight() gets rid of those.
				name += "/" + strings.TrimRight(string(bytes[0:nameLen]), "\000")
			}

			event := newEvent(name, mask)

			// Send the events that are not ignored on the events channel
			if !event.ignoreLinux(mask) {
				select {
				case w.Events <- event:
				case <-w.done:
					return
				}
			}

			// Move to the next event in the buffer
			offset += syscall.SizeofInotifyEvent + nameLen
		}
	}
}

// Certain types of events can be "ignored" and not sent over the Events
// channel. Such as events marked ignore by the kernel, or MODIFY events
// against files that do not exist.
func (e *Event) ignoreLinux(mask uint32) bool {
	// Ignore anything the inotify API says to ignore
	if mask&syscall.IN_IGNORED == syscall.IN_IGNORED {
		return true
	}

	// If the event is not a DELETE or RENAME, the file must exist.
	// Otherwise the event is ignored.
	// *Note*: this was put in place because it was seen that a MODIFY
	// event was sent after the DELETE. This ignores that MODIFY and
	// assumes a DELETE will come or has come if the file doesn't exist.
	if !(e.Op&Remove == Remove || e.Op&Rename == Rename) {
		_, statErr := os.Lstat(e.Name)
		return os.IsNotExist(statErr)
	}
	return false
}

// newEvent returns an platform-independent Event based on an inotify mask.
func newEvent(name string, mask uint32) Event {
	e := Event{Name: name}
	if mask&syscall.IN_CREATE == syscall.IN_CREATE || mask&syscall.IN_MOVED_TO == syscall.IN_MOVED_TO {
		e.Op |= Create
	}
	if mask&syscall.IN_DELETE_SELF == syscall.IN_DELETE_SELF || mask&syscall.IN_DELETE == syscall.IN_DELETE {
		e.Op |= Remove
	}
	if mask&syscall.IN_MODIFY == syscall.IN_MODIFY {
		e.Op |= Write
	}
	if mask&syscall.IN_MOVE_SELF == syscall.IN_MOVE_SELF || mask&syscall.IN_MOVED_FROM == syscall.IN_MOVED_FROM {
		e.Op |= Rename
	}
	if mask&syscall.IN_ATTRIB == syscall.IN_ATTRIB {
		e.Op |= Chmod
	}
	return e
}
