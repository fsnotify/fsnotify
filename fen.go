// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build solaris

package fsnotify

// #include <errno.h>
// #include <strings.h>
// #include <unistd.h>
// #include <stdio.h>
// #include <port.h>
// #include <sys/stat.h>
//
// uintptr_t from_file_obj(struct file_obj *obj) {
//   return (uintptr_t)obj;
// }
//
// struct file_obj*to_file_obj(uintptr_t ptr) {
//   return (struct file_obj*)ptr;
// }
//
// struct file_info {
//   uint mode;
// };
import "C"
import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// Watcher watches a set of files, delivering events to a channel.
type Watcher struct {
	Events chan Event
	Errors chan error

	port C.int
	wg   *sync.WaitGroup

	mu      sync.Mutex
	watches map[string]*C.struct_file_obj

	closed bool
}

// NewWatcher establishes a new watcher with the underlying OS and begins waiting for events.
func NewWatcher() (*Watcher, error) {
	var err error

	w := new(Watcher)
	w.Events = make(chan Event)
	w.Errors = make(chan error)
	w.watches = make(map[string]*C.struct_file_obj)

	w.port, err = C.port_create()
	if err != nil {
		return nil, err
	}

	w.wg = new(sync.WaitGroup)
	w.wg.Add(1)
	go w.run()

	return w, nil
}

// Close removes all watches and closes the events channel.
func (w *Watcher) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	C.close(w.port)
	w.wg.Wait()

	close(w.Events)
	close(w.Errors)
	return nil
}

// Add starts watching the named file or directory (non-recursively).
func (w *Watcher) Add(path string) error {
	stat, err := os.Stat(path)
	switch {
	case err != nil:
		return err
	case stat.IsDir():
		return w.handleDirectory(path, stat, w.associateFile)
	default:
		return w.associateFile(path, stat)
	}
}

// Remove stops watching the named file or directory (non-recursively).
func (w *Watcher) Remove(path string) error {
	if !w.watched(path) {
		return nil
	}

	stat, err := os.Stat(path)
	switch {
	case err != nil:
		return err
	case stat.IsDir():
		return w.handleDirectory(path, stat, w.dissociateFile)
	default:
		return w.dissociateFile(path, stat)
	}
}

func (w *Watcher) run() {
	defer w.wg.Done()

	for {
		var pevent C.port_event_t
		_, err := C.port_get(w.port, &pevent, nil)
		if err != nil {
			if err.(syscall.Errno) == C.EBADF {
				return
			}
			w.Errors <- err
		}

		if pevent.portev_source != C.PORT_SOURCE_FILE {
			// Event from unexpected source received; should never happen.
			continue
		}

		finfo := (*C.struct_file_info)(pevent.portev_user)
		err = w.handleEvent(pevent.portev_object, pevent.portev_events, finfo)
		if err != nil {
			w.Errors <- err
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
		err := handler(filepath.Join(path, finfo.Name()), finfo)
		if err != nil {
			return err
		}
	}

	// And finally handle the directory itself.
	return handler(path, stat)
}

func (w *Watcher) handleEvent(obj C.uintptr_t, events C.int, finfo *C.struct_file_info) error {
	fobj := C.to_file_obj(obj)
	path := C.GoString(fobj.fo_name)
	fmode := os.FileMode(finfo.mode)

	switch {
	case events&C.FILE_MODIFIED == C.FILE_MODIFIED:
		if fmode.IsDir() {
			if err := w.updateDirectory(path); err != nil {
				return err
			}
		} else {
			w.Events <- Event{path, Write}
		}
	case events&C.FILE_ATTRIB == C.FILE_ATTRIB:
		w.Events <- Event{path, Chmod}
	case events&C.FILE_DELETE == C.FILE_DELETE:
		w.unwatch(path)
		w.Events <- Event{path, Remove}
		return nil
	case events&C.FILE_RENAME_TO == C.FILE_RENAME_TO:
		w.Events <- Event{path, Rename}
	case events&C.FILE_RENAME_FROM == C.FILE_RENAME_FROM:
		w.Events <- Event{path, Rename}
		w.unwatch(path)
		// as the file was renamed to something else, the new
		// file (based on something watched) will be be ignored
		return nil
	default:
		return errors.New("unknown event received")
	}

	stat, err := os.Stat(path)
	if err != nil {
		return err
	}

	return w.associateFile(path, stat)
}

func (w *Watcher) updateDirectory(path string) error {
	// The directory was modified, find unwatched entites and
	// watch them. If something was removed from the directory
	// nothing will happen, as everything else should still be
	// watched.
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}

	for _, finfo := range files {
		path := filepath.Join(path, finfo.Name())
		if w.watched(path) {
			continue
		}

		err := w.associateFile(path, finfo)
		if err != nil {
			w.Errors <- err
		}
		w.Events <- Event{path, Create}
	}
	return nil
}

func (w *Watcher) associateFile(path string, stat os.FileInfo) error {
	fobj := buildFileObj(path, stat)
	w.watch(path, &fobj)

	var finfo C.struct_file_info
	finfo.mode = C.uint(stat.Mode())

	mode := C.FILE_MODIFIED | C.FILE_ATTRIB | C.FILE_NOFOLLOW

	_, err := C.port_associate(w.port, C.PORT_SOURCE_FILE, C.from_file_obj(&fobj), mode, unsafe.Pointer(&finfo))
	return err
}

func (w *Watcher) dissociateFile(path string, stat os.FileInfo) error {
	if !w.watched(path) {
		return nil
	}
	fobj := w.unwatch(path)

	_, err := C.port_dissociate(w.port, C.PORT_SOURCE_FILE, C.from_file_obj(fobj))
	return err
}

func buildFileObj(path string, stat os.FileInfo) C.struct_file_obj {
	var fobj C.struct_file_obj
	fobj.fo_name = C.CString(path)
	fobj.fo_atime.tv_sec = C.time_t(stat.Sys().(*syscall.Stat_t).Atim.Sec)
	fobj.fo_atime.tv_nsec = C.long(stat.Sys().(*syscall.Stat_t).Atim.Nsec)

	fobj.fo_mtime.tv_sec = C.time_t(stat.Sys().(*syscall.Stat_t).Mtim.Sec)
	fobj.fo_mtime.tv_nsec = C.long(stat.Sys().(*syscall.Stat_t).Mtim.Nsec)

	fobj.fo_ctime.tv_sec = C.time_t(stat.Sys().(*syscall.Stat_t).Ctim.Sec)
	fobj.fo_ctime.tv_nsec = C.long(stat.Sys().(*syscall.Stat_t).Ctim.Nsec)
	return fobj
}

func (w *Watcher) watched(path string) bool {
	w.mu.Lock()
	_, found := w.watches[path]
	w.mu.Unlock()
	return found
}

func (w *Watcher) unwatch(path string) *C.struct_file_obj {
	w.mu.Lock()
	fobj := w.watches[path]
	delete(w.watches, path)
	w.mu.Unlock()
	return fobj
}

func (w *Watcher) watch(path string, fobj *C.struct_file_obj) {
	w.mu.Lock()
	w.watches[path] = fobj
	w.mu.Unlock()
}
