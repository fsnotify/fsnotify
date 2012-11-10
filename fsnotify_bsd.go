// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd openbsd netbsd darwin

//Package fsnotify implements filesystem notification.
package fsnotify

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
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
func (e *FileEvent) IsModify() bool {
	return ((e.mask&NOTE_WRITE) == NOTE_WRITE || (e.mask&NOTE_ATTRIB) == NOTE_ATTRIB)
}

// IsRename reports whether the FileEvent was triggerd by a change name
func (e *FileEvent) IsRename() bool { return (e.mask & NOTE_RENAME) == NOTE_RENAME }

type Watcher struct {
	kq            int                 // File descriptor (as returned by the kqueue() syscall)
	watches       map[string]int      // Map of watched file diescriptors (key: path)
	fsnFlags      map[string]uint32   // Map of watched files to flags used for filter
	enFlags       map[string]uint32   // Map of watched files to evfilt note flags used in kqueue
	paths         map[int]string      // Map of watched paths (key: watch descriptor)
	finfo         map[int]os.FileInfo // Map of file information (isDir, isReg; key: watch descriptor)
	fileExists    map[string]bool     // Keep track of if we know this file exists (to stop duplicate create events)
	Error         chan error          // Errors are sent on this channel
	internalEvent chan *FileEvent     // Events are queued on this channel
	Event         chan *FileEvent     // Events are returned on this channel
	done          chan bool           // Channel for sending a "quit message" to the reader goroutine
	isClosed      bool                // Set to true when Close() is first called
	kbuf          [1]syscall.Kevent_t // An event buffer for Add/Remove watch
}

// NewWatcher creates and returns a new kevent instance using kqueue(2)
func NewWatcher() (*Watcher, error) {
	fd, errno := syscall.Kqueue()
	if fd == -1 {
		return nil, os.NewSyscallError("kqueue", errno)
	}
	w := &Watcher{
		kq:            fd,
		watches:       make(map[string]int),
		fsnFlags:      make(map[string]uint32),
		enFlags:       make(map[string]uint32),
		paths:         make(map[int]string),
		finfo:         make(map[int]os.FileInfo),
		fileExists:    make(map[string]bool),
		internalEvent: make(chan *FileEvent),
		Event:         make(chan *FileEvent),
		Error:         make(chan error),
		done:          make(chan bool, 1),
	}

	go w.readEvents()
	go w.purgeEvents()
	return w, nil
}

// Close closes a kevent watcher instance
// It sends a message to the reader goroutine to quit and removes all watches
// associated with the kevent instance
func (w *Watcher) Close() error {
	if w.isClosed {
		return nil
	}
	w.isClosed = true

	// Send "quit" message to the reader goroutine
	w.done <- true
	for path := range w.watches {
		w.removeWatch(path)
	}

	return nil
}

// AddWatch adds path to the watched file set.
// The flags are interpreted as described in kevent(2).
func (w *Watcher) addWatch(path string, flags uint32) error {
	if w.isClosed {
		return errors.New("kevent instance already closed")
	}

	watchDir := false

	watchfd, found := w.watches[path]
	if !found {
		fi, errstat := os.Lstat(path)
		if errstat != nil {
			return errstat
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
			path, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}

			fi, errstat = os.Lstat(path)
			if errstat != nil {
				return nil
			}
		}

		fd, errno := syscall.Open(path, syscall.O_NONBLOCK|syscall.O_RDONLY, 0700)
		if fd == -1 {
			return errno
		}
		watchfd = fd

		w.watches[path] = watchfd
		w.paths[watchfd] = path

		w.finfo[watchfd] = fi
	}
	// Watch the directory if it has not been watched before.
	if w.finfo[watchfd].IsDir() &&
		(flags&NOTE_WRITE) == NOTE_WRITE &&
		(!found || (w.enFlags[path]&NOTE_WRITE) != NOTE_WRITE) {
		watchDir = true
	}

	w.enFlags[path] = flags
	watchEntry := &w.kbuf[0]
	watchEntry.Fflags = flags
	syscall.SetKevent(watchEntry, watchfd, syscall.EVFILT_VNODE, syscall.EV_ADD|syscall.EV_CLEAR)

	wd, errno := syscall.Kevent(w.kq, w.kbuf[:], nil, nil)
	if wd == -1 {
		return errno
	} else if (watchEntry.Flags & syscall.EV_ERROR) == syscall.EV_ERROR {
		return errors.New("kevent add error")
	}

	if watchDir {
		errdir := w.watchDirectoryFiles(path)
		if errdir != nil {
			return errdir
		}
	}
	return nil
}

// Watch adds path to the watched file set, watching all events.
func (w *Watcher) watch(path string) error {
	return w.addWatch(path, NOTE_ALLEVENTS)
}

// RemoveWatch removes path from the watched file set.
func (w *Watcher) removeWatch(path string) error {
	watchfd, ok := w.watches[path]
	if !ok {
		return errors.New(fmt.Sprintf("can't remove non-existent kevent watch for: %s", path))
	}
	watchEntry := &w.kbuf[0]
	syscall.SetKevent(watchEntry, w.watches[path], syscall.EVFILT_VNODE, syscall.EV_DELETE)
	success, errno := syscall.Kevent(w.kq, w.kbuf[:], nil, nil)
	if success == -1 {
		return os.NewSyscallError("kevent_rm_watch", errno)
	} else if (watchEntry.Flags & syscall.EV_ERROR) == syscall.EV_ERROR {
		return errors.New("kevent rm error")
	}
	syscall.Close(watchfd)
	delete(w.watches, path)
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
		errno    error                // Syscall errno
	)
	events = eventbuf[0:0]
	twait = new(syscall.Timespec)
	*twait = syscall.NsecToTimespec(keventWaitTime)

	for {
		// See if there is a message on the "done" channel
		var done bool
		select {
		case done = <-w.done:
		default:
		}

		// If "done" message is received
		if done {
			errno := syscall.Close(w.kq)
			if errno != nil {
				w.Error <- os.NewSyscallError("close", errno)
			}
			close(w.internalEvent)
			close(w.Error)
			return
		}

		// Get new events
		if len(events) == 0 {
			n, errno = syscall.Kevent(w.kq, nil, eventbuf[:], twait)

			// EINTR is okay, basically the syscall was interrupted before
			// timeout expired.
			if errno != nil && errno != syscall.EINTR {
				w.Error <- os.NewSyscallError("kevent", errno)
				continue
			}

			// Received some events
			if n > 0 {
				events = eventbuf[0:n]
			}
		}

		// Flush the events we recieved to the events channel
		for len(events) > 0 {
			fileEvent := new(FileEvent)
			watchEvent := &events[0]
			fileEvent.mask = uint32(watchEvent.Fflags)
			fileEvent.Name = w.paths[int(watchEvent.Ident)]

			fileInfo := w.finfo[int(watchEvent.Ident)]
			if fileInfo.IsDir() && !fileEvent.IsDelete() {
				// Double check to make sure the directory exist. This can happen when
				// we do a rm -fr on a recursively watched folders and we receive a
				// modification event first but the folder has been deleted and later
				// receive the delete event
				if _, err := os.Lstat(fileEvent.Name); os.IsNotExist(err) {
					// mark is as delete event
					fileEvent.mask |= NOTE_DELETE
				}
			}

			if fileInfo.IsDir() && fileEvent.IsModify() && !fileEvent.IsDelete() {
				w.sendDirectoryChangeEvents(fileEvent.Name)
			} else {
				// Send the event on the events channel
				w.internalEvent <- fileEvent
			}

			// Move to next event
			events = events[1:]

			if fileEvent.IsRename() {
				w.removeWatch(fileEvent.Name)
				delete(w.fileExists, fileEvent.Name)
			}
			if fileEvent.IsDelete() {
				w.removeWatch(fileEvent.Name)
				delete(w.fileExists, fileEvent.Name)

				// Look for a file that may have overwritten this
				// (ie mv f1 f2 will delete f2 then create f2)
				fileDir, _ := filepath.Split(fileEvent.Name)
				fileDir = filepath.Clean(fileDir)
				if _, found := w.watches[fileDir]; found {
					// make sure the directory exist before we watch for changes. When we
					// do a recursive watch and perform rm -fr, the parent directory might
					// have gone missing, ignore the missing directory and let the
					// upcoming delete event remove the watch form the parent folder
					if _, err := os.Lstat(fileDir); !os.IsNotExist(err) {
						w.sendDirectoryChangeEvents(fileDir)
					}
				}
			}
		}
	}
}

func (w *Watcher) watchDirectoryFiles(dirPath string) error {
	// Get all files
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return err
	}

	// Search for new files
	for _, fileInfo := range files {
		filePath := filepath.Join(dirPath, fileInfo.Name())
		if fileInfo.IsDir() == false {
			// Watch file to mimic linux fsnotify
			e := w.addWatch(filePath, NOTE_DELETE|NOTE_WRITE|NOTE_RENAME)
			w.fsnFlags[filePath] = FSN_ALL
			if e != nil {
				return e
			}
		} else {
			// If the user is currently waching directory
			// we want to preserve the flags used
			currFlags, found := w.enFlags[filePath]
			var newFlags uint32 = NOTE_DELETE
			if found {
				newFlags |= currFlags
			}

			// Linux gives deletes if not explicitly watching
			e := w.addWatch(filePath, newFlags)
			w.fsnFlags[filePath] = FSN_ALL
			if e != nil {
				return e
			}
		}
		w.fileExists[filePath] = true
	}

	return nil
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
		filePath := filepath.Join(dirPath, fileInfo.Name())
		_, doesExist := w.fileExists[filePath]
		if doesExist == false {
			w.fsnFlags[filePath] = FSN_ALL
			// Send create event
			fileEvent := new(FileEvent)
			fileEvent.Name = filePath
			fileEvent.create = true
			w.internalEvent <- fileEvent
		}
		w.fileExists[filePath] = true
	}
	w.watchDirectoryFiles(dirPath)
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
