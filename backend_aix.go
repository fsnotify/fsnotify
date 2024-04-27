//go:build aix

// Copyright 2022 Power-Devops.com. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/power-devops/ahafs"
	"golang.org/x/sys/unix"
)

// Watcher watches a set of paths, delivering events on a channel.
//
// A watcher should not be copied (e.g. pass it by pointer, rather than by
// value).
//
// # Linux notes
//
// When a file is removed a Remove event won't be emitted until all file
// descriptors are closed, and deletes will always emit a Chmod. For example:
//
//	fp := os.Open("file")
//	os.Remove("file")        // Triggers Chmod
//	fp.Close()               // Triggers Remove
//
// This is the event that inotify sends, so not much can be changed about this.
//
// The fs.inotify.max_user_watches sysctl variable specifies the upper limit
// for the number of watches per user, and fs.inotify.max_user_instances
// specifies the maximum number of inotify instances per user. Every Watcher you
// create is an "instance", and every path you add is a "watch".
//
// These are also exposed in /proc as /proc/sys/fs/inotify/max_user_watches and
// /proc/sys/fs/inotify/max_user_instances
//
// To increase them you can use sysctl or write the value to the /proc file:
//
//	# Default values on Linux 5.18
//	sysctl fs.inotify.max_user_watches=124983
//	sysctl fs.inotify.max_user_instances=128
//
// To make the changes persist on reboot edit /etc/sysctl.conf or
// /usr/lib/sysctl.d/50-default.conf (details differ per Linux distro; check
// your distro's documentation):
//
//	fs.inotify.max_user_watches=124983
//	fs.inotify.max_user_instances=128
//
// Reaching the limit will result in a "no space left on device" or "too many open
// files" error.
//
// # kqueue notes (macOS, BSD)
//
// kqueue requires opening a file descriptor for every file that's being watched;
// so if you're watching a directory with five files then that's six file
// descriptors. You will run in to your system's "max open files" limit faster on
// these platforms.
//
// The sysctl variables kern.maxfiles and kern.maxfilesperproc can be used to
// control the maximum number of open files, as well as /etc/login.conf on BSD
// systems.
//
// # macOS notes
//
// Spotlight indexing on macOS can result in multiple events (see [#15]). A
// temporary workaround is to add your folder(s) to the "Spotlight Privacy
// Settings" until we have a native FSEvents implementation (see [#11]).
//
// [#11]: https://github.com/fsnotify/fsnotify/issues/11
// [#15]: https://github.com/fsnotify/fsnotify/issues/15
type Watcher struct {
	// Events sends the filesystem change events.
	//
	// fsnotify can send the following events; a "path" here can refer to a
	// file, directory, symbolic link, or special file like a FIFO.
	//
	//   fsnotify.Create    A new path was created; this may be followed by one
	//                      or more Write events if data also gets written to a
	//                      file.
	//
	//   fsnotify.Remove    A path was removed.
	//
	//   fsnotify.Rename    A path was renamed. A rename is always sent with the
	//                      old path as Event.Name, and a Create event will be
	//                      sent with the new name. Renames are only sent for
	//                      paths that are currently watched; e.g. moving an
	//                      unmonitored file into a monitored directory will
	//                      show up as just a Create. Similarly, renaming a file
	//                      to outside a monitored directory will show up as
	//                      only a Rename.
	//
	//   fsnotify.Write     A file or named pipe was written to. A Truncate will
	//                      also trigger a Write. A single "write action"
	//                      initiated by the user may show up as one or multiple
	//                      writes, depending on when the system syncs things to
	//                      disk. For example when compiling a large Go program
	//                      you may get hundreds of Write events, so you
	//                      probably want to wait until you've stopped receiving
	//                      them (see the dedup example in cmd/fsnotify).
	//
	//   fsnotify.Chmod     Attributes were changed. On Linux this is also sent
	//                      when a file is removed (or more accurately, when a
	//                      link to an inode is removed). On kqueue it's sent
	//                      and on kqueue when a file is truncated. On Windows
	//                      it's never sent.
	Events chan Event
	// Errors sends any errors.
	//
	// [ErrEventOverflow] is used to indicate ther are too many events:
	//
	//  - inotify: there are too many queued events (fs.inotify.max_queued_events sysctl)
	//  - windows: The buffer size is too small.
	//  - kqueue, fen: not used.
	Errors  chan error
	mu      sync.Mutex
	watches map[string]*watch // Map of ahafs watches (key: path)
}

type watch struct {
	path    string
	fileevt *ahafs.Monitor
	filec   chan ahafs.Event
	attrevt *ahafs.Monitor
	attrc   chan ahafs.Event
	pathevt *ahafs.Monitor
	pathc   chan ahafs.Event
}

// NewWatcher creates a new Watcher.
func NewWatcher() (*Watcher, error) {
	if !isAhaMounted() {
		return nil, errors.New("AHAFS is not mounted")
	}
	w := &Watcher{
		Events:  make(chan Event),
		Errors:  make(chan error),
		watches: make(map[string]*watch),
	}
	go w.readEvents()
	return w, nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	for _, x := range w.watches {
		x.Close()
	}
	return nil
}

// Add starts monitoring the path for changes.
//
// A path can only be watched once; attempting to watch it more than once will
// return an error. Paths that do not yet exist on the filesystem cannot be
// added.
//
// A watch will be automatically removed if the watched path is deleted or
// renamed. The exception is the Windows backend, which doesn't remove the
// watcher on renames.
//
// Notifications on network filesystems (NFS, SMB, FUSE, etc.) or special
// filesystems (/proc, /sys, etc.) generally don't work.
//
// Returns [ErrClosed] if [Watcher.Close] was called.
//
// # Watching directories
//
// All files in a directory are monitored, including new files that are created
// after the watcher is started. Subdirectories are not watched (i.e. it's
// non-recursive).
//
// # Watching files
//
// Watching individual files (rather than directories) is generally not
// recommended as many tools update files atomically. Instead of "just" writing
// to the file a temporary file will be written to first, and if successful the
// temporary file is moved to to destination removing the original, or some
// variant thereof. The watcher on the original file is now lost, as it no
// longer exists.
//
// Instead, watch the parent directory and use Event.Name to filter out files
// you're not interested in. There is an example of this in [cmd/fsnotify/file.go].
func (w *Watcher) Add(name string) error {
	name = filepath.Clean(name)
	w.mu.Lock()
	defer w.mu.Unlock()
	wa := w.watches[name]
	if wa != nil {
		return fmt.Errorf("Object %s is already monitored", name)
	}
	wa, err := newWatch(name)
	if err != nil {
		return err
	}
	w.watches[name] = wa
	return nil
}

// Remove stops monitoring the path for changes.
//
// Directories are always removed non-recursively. For example, if you added
// /tmp/dir and /tmp/dir/subdir then you will need to remove both.
//
// Removing a path that has not yet been added returns [ErrNonExistentWatch].
func (w *Watcher) Remove(name string) error {
	name = filepath.Clean(name)
	w.mu.Lock()
	defer w.mu.Unlock()
	wa := w.watches[name]
	if wa == nil {
		return fmt.Errorf("Object %s is not monitored", name)
	}
	wa.Close()
	delete(w.watches, name)
	return nil
}

// WatchList returns all paths added with [Add] (and are not yet removed).
//
// Returns nil if [Watcher.Close] was called.
func (w *Watcher) WatchList() []string {
	// TODO: not implemented.
	return nil
}

// readEvents reads events from ahafs, converts the received events into Event
// objects and sends them via the Events channel
func (w *Watcher) readEvents() {
	c := make(chan Event, 1)
	for _, wa := range w.watches {
		go wa.GetEvents(c)
	}
	for {
		select {
		case ahaevt, ok := <-c:
			if !ok {
				continue
			}
			w.Events <- ahaevt
		}
	}
}

func newWatch(name string) (*watch, error) {
	w := &watch{path: name}
	// fmt.Println("Adding a new watcher for:", name)
	s, err := os.Stat(name)
	switch {
	case err == nil && !s.IsDir():
		// fmt.Println("We should monitor an existing file")
		// fmt.Println("Adding file monitor for:", name)
		if fm, err := ahafs.NewFileMonitor(name); err != nil {
			return nil, err
		} else {
			w.fileevt = fm
		}
		// fmt.Println("Adding attribute monitor for:", name)
		if am, err := ahafs.NewFileAttrMonitor(name); err != nil {
			return nil, err
		} else {
			w.attrevt = am
		}
		// fmt.Println("Adding directory monitor for:", filepath.Dir(name))
		if pm, err := ahafs.NewDirMonitor(filepath.Dir(name)); err != nil {
			return nil, err
		} else {
			w.pathevt = pm
		}
	case err == nil && s.IsDir():
		// fmt.Println("We should monitor an existing directory")
		// fmt.Println("Adding directory monitor for:", name)
		if pm, err := ahafs.NewDirMonitor(name); err != nil {
			return nil, err
		} else {
			w.pathevt = pm
		}
	case err != nil:
		// fmt.Println("We should monitor something non-existent. Assume it is a file")
		// fmt.Println("Adding directory monitor for:", filepath.Dir(name))
		if pm, err := ahafs.NewDirMonitor(filepath.Dir(name)); err != nil {
			return nil, err
		} else {
			w.pathevt = pm
		}
	}
	return w, nil
}

// GetEvents returns the newest events from the specific watcher
func (w *watch) GetEvents(c chan<- Event) {
	filecAlive := false
	pathcAlive := false
	attrcAlive := false
	w.filec = make(chan ahafs.Event)
	w.pathc = make(chan ahafs.Event)
	w.attrc = make(chan ahafs.Event)
	defer func() {
		if !isClosed(w.filec) {
			close(w.filec)
		}
		if !isClosed(w.pathc) {
			close(w.pathc)
		}
		if !isClosed(w.attrc) {
			close(w.attrc)
		}
	}()
	if w.fileevt != nil {
		filecAlive = true
		go w.fileevt.Watch(w.filec)
	}
	if w.pathevt != nil {
		pathcAlive = true
		go w.pathevt.Watch(w.pathc)
	}
	if w.attrevt != nil {
		attrcAlive = true
		go w.attrevt.Watch(w.attrc)
	}
	for {
		var e ahafs.Event
		var ok bool
		select {
		case e, ok = <-w.filec:
			// fmt.Println("Event from file channel:", e)
			if !ok {
				continue
			}
			if !filecAlive {
				continue
			}
			if e.IsQuit() {
				filecAlive = false
				w.fileevt.Close()
				continue
			}
			select {
			case c <- Event{Name: w.path, Op: Write}:
			default:
			}
		case e, ok = <-w.pathc:
			// fmt.Println("Event from path channel:", e)
			if !ok {
				continue
			}
			if !pathcAlive {
				continue
			}
			if e.IsQuit() {
				pathcAlive = false
				w.pathevt.Close()
				continue
			}
			switch e.RC {
			case ahafs.ModDirCreate:
				// a file is created
				// fmt.Println("File created")
				if w.fileevt == nil {
					if _, err := os.Stat(w.path); err == nil {
						if fm, err := ahafs.NewFileMonitor(w.path); err != nil {
							fmt.Println("Can't create file monitor:", err)
							continue
						} else {
							w.fileevt = fm
							filecAlive = true
							go w.fileevt.Watch(w.filec)
						}
					}
				}
				if w.attrevt == nil {
					if _, err := os.Stat(w.path); err == nil {
						if am, err := ahafs.NewFileAttrMonitor(w.path); err != nil {
							fmt.Println("Can't create attribute monitor:", err)
							continue
						} else {
							w.attrevt = am
							attrcAlive = true
							go w.attrevt.Watch(w.attrc)
						}
					}
				}
				select {
				case c <- Event{Name: w.path, Op: Create}:
				default:
				}
			case ahafs.ModDirRemove:
				// a file is removed
				// fmt.Println("File removed")
				if strings.Split(e.Info, "\n")[0] == w.path {
					// the file is removed, no events more from filec and attrc
					filecAlive = false
					attrcAlive = false
				}
				select {
				case c <- Event{Name: w.path, Op: Remove}:
				default:
				}
			}
		case e, ok = <-w.attrc:
			// fmt.Println("Event from attribute channel:", e)
			if !ok {
				continue
			}
			if !attrcAlive {
				continue
			}
			if e.IsQuit() {
				attrcAlive = false
				w.attrevt.Close()
				continue
			}
			switch e.RC {
			case ahafs.ModFileAttrRemove:
				select {
				case c <- Event{Name: w.path, Op: Remove}:
				default:
				}
				// file is removed, no events anymore from filec & attrc
				filecAlive = false
				attrcAlive = false
				w.attrevt.Close()
				w.fileevt.Close()
				continue
			case ahafs.ModFileAttrRename:
				select {
				case c <- Event{Name: w.path, Op: Rename}:
				default:
				}
				continue
			}
			select {
			case c <- Event{Name: w.path, Op: Chmod}:
			default:
			}
		}
	}
}

func (w *watch) Close() {
	if w.fileevt != nil {
		w.fileevt.Close()
	}
	if w.pathevt != nil {
		w.pathevt.Close()
	}
	if w.attrevt != nil {
		w.attrevt.Close()
	}
}

func isAhaMounted() bool {
	st := unix.Stat_t{}
	err := unix.Stat("/aha", &st)
	if err != nil {
		return false
	}
	if st.Flag != 1 {
		return false
	}
	return true
}

func isClosed(ch <-chan ahafs.Event) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}
