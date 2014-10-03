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

const EPOLL_MAX_EVENTS = 16

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events    chan Event
	Errors    chan error
	mu        sync.Mutex        // Map access
	cv        *sync.Cond        // sync removing on rm_watch with IN_IGNORE
	fd        int               // File descriptor (as returned by the inotify_init() syscall)
	watches   map[string]*watch // Map of inotify watches (key: path)
	paths     map[int]string    // Map of watched paths (key: watch descriptor)
	done      chan bool         // Channel for sending a "quit message" to the reader goroutine
	isClosed  bool              // Set to true when Close() is first called
	isRunning bool              // epollEvents go routine is running or not
	closed    chan bool         // Channel for syncing Close()
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
		done:    make(chan bool),
		closed:  make(chan bool),
	}
	w.cv = sync.NewCond(&w.mu)

	rp, wp, err := os.Pipe() // for done
	if err != nil {
		return nil, err
	}
	epfd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, os.NewSyscallError("epoll_create1", err)
	}
	event := &syscall.EpollEvent{syscall.EPOLLIN, int32(w.fd), 0}
	if err = syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, w.fd, event); err != nil {
		return nil, os.NewSyscallError("epoll_ctl", err)
	}
	event = &syscall.EpollEvent{syscall.EPOLLIN, int32(rp.Fd()), 0}
	if err = syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, int(rp.Fd()), event); err != nil {
		return nil, os.NewSyscallError("epoll_ctl", err)
	}

	go func() {
		<-w.done
		wp.Close() // make rp readable
	}()
	go w.epollEvents(epfd, rp)

	return w, nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	if w.isClosed {
		return nil
	}
	w.isClosed = true

	// Send "quit" message to the reader goroutine
	w.done <- true
	// And wait receiving it's actually closed
	<-w.closed

	w.watches = nil
	w.paths = nil
	return nil
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(name string) error {
	name = filepath.Clean(name)
	if w.isClosed {
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

// Remove stops watching the the named file or directory (non-recursively).
func (w *Watcher) Remove(name string) error {
	name = filepath.Clean(name)
	w.mu.Lock()
	defer w.mu.Unlock()
	watch, ok := w.watches[name]
	if !ok {
		return fmt.Errorf("can't remove non-existent inotify watch for: %s", name)
	}
	success, errno := syscall.InotifyRmWatch(w.fd, watch.wd)
	if success == -1 {
		return os.NewSyscallError("inotify_rm_watch", errno)
	}

	// not delete(w.watches, name) here, but in ignoreLinux()
	// use condv to sync with ignoreLinux()
	for ok {
		w.cv.Wait()
		_, ok = w.watches[name]
	}

	return nil
}

type watch struct {
	wd    uint32 // Watch descriptor (as returned by the inotify_add_watch() syscall)
	flags uint32 // inotify flags of this watch (see inotify(7) for the list of valid flags)
}

// readEvents reads from the inotify file descriptor, converts the
// received events into Event objects and sends them via the Events channel
func (w *Watcher) readEvents() error {
	var (
		buf   [syscall.SizeofInotifyEvent * 4096]byte // Buffer for a maximum of 4096 raw events
		n     int                                     // Number of bytes read with read()
		errno error                                   // Syscall errno
	)

	n, errno = syscall.Read(w.fd, buf[:])
	switch {
	case n < 0:
		return os.NewSyscallError("read", errno)
	case n == 0:
		return errors.New("inotify: possibilly too many events occurred at once")
	case n < syscall.SizeofInotifyEvent:
		return errors.New("inotify: short read in readEvents()")
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
		if !event.ignoreLinux(w, raw.Wd, mask) {
			w.Events <- event
		}

		// Move to the next event in the buffer
		offset += syscall.SizeofInotifyEvent + nameLen
	}

	return nil
}

// Certain types of events can be "ignored" and not sent over the Events
// channel. Such as events marked ignore by the kernel, or MODIFY events
// against files that do not exist.
func (e *Event) ignoreLinux(w *Watcher, wd int32, mask uint32) bool {
	// Ignore anything the inotify API says to ignore
	if mask&syscall.IN_IGNORED == syscall.IN_IGNORED {
		w.mu.Lock()
		defer w.mu.Unlock()
		name := w.paths[int(wd)]
		delete(w.paths, int(wd))
		delete(w.watches, name)
		w.cv.Broadcast()
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

func (w *Watcher) length() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.watches) != len(w.paths) {
		panic("internal maps lengh is differ")
	}
	return len(w.watches)
}

func (w *Watcher) epollEvents(epfd int, donePipe *os.File) {
	w.isRunning = true
	defer func() {
		syscall.Close(epfd)
		w.isRunning = false
		w.closed <- true
	}()
	events := make([]syscall.EpollEvent, EPOLL_MAX_EVENTS)
	doneFd := int32(donePipe.Fd())
	for {
		nevents, err := syscall.EpollWait(epfd, events, -1)
		if err != nil {
			w.Errors <- os.NewSyscallError("epoll_wait", err)
			continue
		}
		if nevents == 0 {
			continue
		}

		for i := 0; i < nevents; i++ {
			if events[i].Fd == doneFd {
				if err = donePipe.Close(); err != nil {
					w.Errors <- err
				}
				syscall.Close(w.fd)
				close(w.done)
				close(w.Events)
				close(w.Errors)
				return
			} else if events[i].Fd != int32(w.fd) {
				continue
			}
			if err = w.readEvents(); err != nil {
				w.Errors <- err
			}
		}
	}
}
