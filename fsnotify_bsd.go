// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package fsnotify implements filesystem notification.

Example:
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        log.Fatal(err)
    }
    err = watcher.Watch("/tmp")
    if err != nil {
        log.Fatal(err)
    }
    for {
        select {
        case ev := <-watcher.Event:
            log.Println("event:", ev)
        case err := <-watcher.Error:
            log.Println("error:", err)
        }
    }

*/
package fsnotify

import (
	"fmt"
	"os"
	"syscall"
	"io/ioutil"
	"path"
)

type FileEvent struct {
	mask   uint32 // Mask of events
	Name   string // File name (optional)
	create bool   // set by fsnotify package if found new file
}

// IsCreate reports whether the FileEvent was triggerd by a creation
func (e *FileEvent) IsCreate() bool { return e.create }

// IsDelete reports whether the FileEvent was triggerd by a delete
func (e *FileEvent) IsDelete() bool { return (e.mask & NOTE_DELETE) == NOTE_DELETE }

// IsModify reports whether the FileEvent was triggerd by a file modification
func (e *FileEvent) IsModify() bool { return (e.mask & NOTE_WRITE) == NOTE_WRITE }

// IsAttribute reports whether the FileEvent was triggerd by a change of attributes
func (e *FileEvent) IsAttribute() bool { return (e.mask & NOTE_ATTRIB) == NOTE_ATTRIB }

// IsRename reports whether the FileEvent was triggerd by a change name
func (e *FileEvent) IsRename() bool { return (e.mask & NOTE_RENAME) == NOTE_RENAME }

type Watcher struct {
	kq       int                  // File descriptor (as returned by the kqueue() syscall)
	watches  map[string]int       // Map of watched file diescriptors (key: path)
	paths    map[int]string       // Map of watched paths (key: watch descriptor)
	finfo    map[int]*os.FileInfo // Map of file information (isDir, isReg; key: watch descriptor)
	Error    chan os.Error        // Errors are sent on this channel
	Event    chan *FileEvent      // Events are returned on this channel
	done     chan bool            // Channel for sending a "quit message" to the reader goroutine
	isClosed bool                 // Set to true when Close() is first called
	kbuf     [1]syscall.Kevent_t  // An event buffer for Add/Remove watch
}

// NewWatcher creates and returns a new kevent instance using kqueue(2)
func NewWatcher() (*Watcher, os.Error) {
	fd, errno := syscall.Kqueue()
	if fd == -1 {
		return nil, os.NewSyscallError("kqueue", errno)
	}
	w := &Watcher{
		kq:      fd,
		watches: make(map[string]int),
		paths:   make(map[int]string),
		finfo:   make(map[int]*os.FileInfo),
		Event:   make(chan *FileEvent),
		Error:   make(chan os.Error),
		done:    make(chan bool, 1),
	}

	go w.readEvents()
	return w, nil
}

// Close closes a kevent watcher instance
// It sends a message to the reader goroutine to quit and removes all watches
// associated with the kevent instance
func (w *Watcher) Close() os.Error {
	if w.isClosed {
		return nil
	}
	w.isClosed = true

	// Send "quit" message to the reader goroutine
	w.done <- true
	for path := range w.watches {
		w.RemoveWatch(path)
	}

	return nil
}

// AddWatch adds path to the watched file set.
// The flags are interpreted as described in kevent(2).
func (w *Watcher) addWatch(path string, flags uint32) os.Error {
	if w.isClosed {
		return os.NewError("kevent instance already closed")
	}

	watchEntry := &w.kbuf[0]
	watchEntry.Fflags = flags

	watchfd, found := w.watches[path]
	if !found {
		fd, errno := syscall.Open(path, syscall.O_NONBLOCK|syscall.O_RDONLY, 0700)
		if fd == -1 {
			return &os.PathError{"kevent_add_watch", path, os.Errno(errno)}
		}
		watchfd = fd

		w.watches[path] = watchfd
		w.paths[watchfd] = path

		fi, _ := os.Stat(path)
		w.finfo[watchfd] = fi
	}
	syscall.SetKevent(watchEntry, watchfd, syscall.EVFILT_VNODE, syscall.EV_ADD|syscall.EV_CLEAR)

	wd, errno := syscall.Kevent(w.kq, w.kbuf[:], nil, nil)
	if wd == -1 {
		return &os.PathError{"kevent_add_watch", path, os.Errno(errno)}
	} else if (watchEntry.Flags & syscall.EV_ERROR) == syscall.EV_ERROR {
		return &os.PathError{"kevent_add_watch", path, os.Errno(int(watchEntry.Data))}
	}

	return nil
}

// Watch adds path to the watched file set, watching all events.
func (w *Watcher) Watch(path string) os.Error {
	return w.addWatch(path, NOTE_ALLEVENTS)
}

// RemoveWatch removes path from the watched file set.
func (w *Watcher) RemoveWatch(path string) os.Error {
	watchfd, ok := w.watches[path]
	if !ok {
		return os.NewError(fmt.Sprintf("can't remove non-existent kevent watch for: %s", path))
	}
	syscall.Close(watchfd)
	watchEntry := &w.kbuf[0]
	syscall.SetKevent(watchEntry, w.watches[path], syscall.EVFILT_VNODE, syscall.EV_DELETE)
	success, errno := syscall.Kevent(w.kq, w.kbuf[:], nil, nil)
	if success == -1 {
		return os.NewSyscallError("kevent_rm_watch", errno)
	} else if (watchEntry.Flags & syscall.EV_ERROR) == syscall.EV_ERROR {
		return os.NewSyscallError("kevent_rm_watch", int(watchEntry.Data))
	}
	w.watches[path] = 0, false
	return nil
}

// readEvents reads from the kqueue file descriptor, converts the
// received events into Event objects and sends them via the Event channel
func (w *Watcher) readEvents() {
	var (
		eventbuf [10]syscall.Kevent_t // Event buffer
		events   []syscall.Kevent_t   // Received events
		twait    *syscall.Timespec    // Time to block waiting for events
		n        int                  // Number of events returned from kevent
		errno    int                  // Syscall errno
	)
	events = eventbuf[0:0]
	twait = new(syscall.Timespec)
	*twait = syscall.NsecToTimespec(keventWaitTime)

	for {
		if len(events) == 0 {
			n, errno = syscall.Kevent(w.kq, nil, eventbuf[:], twait)
			events = eventbuf[0:n]
		}
		// See if there is a message on the "done" channel
		var done bool
		select {
		case done = <-w.done:
		default:
		}

		// If "done" message is received
		if done {
			errno := syscall.Close(w.kq)
			if errno == -1 {
				w.Error <- os.NewSyscallError("close", errno)
			}
			close(w.Event)
			close(w.Error)
			return
		}
		if n < 0 {
			w.Error <- os.NewSyscallError("kevent", errno)
			continue
		}

		// Timeout, no big deal
		if n == 0 {
			continue
		}

		// Flush the events we recieved to the events channel
		for len(events) > 0 {
			fileEvent := new(FileEvent)
			watchEvent := &events[0]
			fileEvent.mask = uint32(watchEvent.Fflags)
			fileEvent.Name = w.paths[int(watchEvent.Ident)]

			fileInfo := w.finfo[int(watchEvent.Ident)]
			if fileInfo.IsDirectory() && fileEvent.IsModify() {
				w.sendDirectoryChangeEvents(fileEvent.Name)
			} else {
				// Send the event on the events channel
				w.Event <- fileEvent
			}

			// Move to next event
			events = events[1:]
		}
	}
}

// sendDirectoryEvents searches the directory for newly created files
// and sends them over the event channel. This functionality is to have
// the BSD version of fsnotify mach linux fsnotify which provides a 
// create event for files created in a watched directory.
func (w *Watcher) sendDirectoryChangeEvents(dirPath string) {
	// Get all files
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		w.Error <- err
	}

	// Search for new files
	for _, fileInfo := range files {
		if fileInfo.IsRegular() == true {
			filePath := path.Join(dirPath, fileInfo.Name)
			if w.watches[filePath] == 0 {
				// Watch file to mimic linux fsnotify
				e := w.addWatch(filePath, NOTE_DELETE|NOTE_WRITE|NOTE_RENAME)
				if e != nil {
					w.Error <- e
				}

				// Send create event
				fileEvent := new(FileEvent)
				fileEvent.Name = filePath
				fileEvent.create = true
				w.Event <- fileEvent
			}
		}
	}
}

const (
	// Flags (from <sys/event.h>)
	NOTE_DELETE = 0x0001 /* vnode was removed */
	NOTE_WRITE  = 0x0002 /* data contents changed */
	NOTE_EXTEND = 0x0004 /* size increased */
	NOTE_ATTRIB = 0x0008 /* attributes changed */
	NOTE_LINK   = 0x0010 /* link count changed */
	NOTE_RENAME = 0x0020 /* vnode was renamed */
	NOTE_REVOKE = 0x0040 /* vnode access was revoked */

	// Watch all events
	NOTE_ALLEVENTS = NOTE_DELETE | NOTE_WRITE | NOTE_ATTRIB | NOTE_RENAME

	// Block for 100 ms on each call to kevent
	keventWaitTime = 100e6
)
