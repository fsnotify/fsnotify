//go:build freebsd || openbsd || netbsd || dragonfly || darwin

package fsnotify

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

type kqueue struct {
	*shared
	Events chan Event
	Errors chan error

	kq        int
	closepipe [2]int // Pipe used for closing kq.
	watches   watches
}

var defaultBufferSize = 0

func newBackend(ev chan Event, errs chan error) (backend, error) {
	kq, closepipe, err := newKqueue()
	if err != nil {
		return nil, err
	}

	w := &kqueue{
		shared:    newShared(ev, errs),
		Events:    ev,
		Errors:    errs,
		kq:        kq,
		closepipe: closepipe,
		watches:   newWatches(),
	}

	go w.readEvents()
	return w, nil
}

func (w *kqueue) Close() error {
	if w.shared.close() {
		return nil
	}

	unix.Close(w.closepipe[1])
	return nil
}

func (w *kqueue) Add(path string) error { return w.AddWith(path) }

func (w *kqueue) AddWith(path string, opts ...addOpt) error {
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), path)
	}

	with := getOptions(opts...)
	if !w.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}

	// We always need at least DELETE and RENAME to know when to stop watching;
	// otherwise we would never get any events for this. These get filtered
	// later.
	fflags := unix.NOTE_DELETE | unix.NOTE_RENAME
	if with.op.Has(Create) {
		// Create is WRITE on parent dir.
		// TODO: probably don't need to set this for files if Create isn't
		// given?
		fflags |= unix.NOTE_WRITE
	}
	if with.op.Has(Write) {
		fflags |= unix.NOTE_WRITE
	}
	if with.op.Has(Chmod) {
		fflags |= unix.NOTE_ATTRIB
	}
	if with.op.Has(xUnportableOpen) {
		fflags |= unix.NOTE_OPEN
	}
	if with.op.Has(xUnportableRead) {
		fflags |= unix.NOTE_READ
	}
	if with.op.Has(xUnportableCloseWrite) {
		fflags |= unix.NOTE_CLOSE_WRITE
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addWatch(path, fflags, with.op, true, true)
}

func (w *kqueue) Remove(path string) error {
	if w.isClosed() {
		return nil
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), path)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	watch, ok := w.watches.byPath(path)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNonExistentWatch, path)
	}
	return w.remove(watch)
}

func (w *kqueue) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	entries := make([]string, 0, 8)
	for _, w := range w.watches.wd {
		if w.byUser {
			entries = append(entries, w.path)
		}
	}
	return entries
}

func (w *kqueue) xSupports(op Op) bool {
	if runtime.GOOS == "freebsd" {
		return !op.Has(xUnportableCloseWrite) // Supports everything except CloseRead.
	}
	if op.Has(xUnportableOpen) || op.Has(xUnportableRead) ||
		op.Has(xUnportableCloseWrite) || op.Has(xUnportableCloseRead) {
		return false
	}
	return true
}

func (w *kqueue) addWatch(path string, fflags int, withOp Op, listDir, fromAdd bool) error {
	if w.isClosed() {
		return ErrClosed
	}

	existing, ok := w.watches.byPath(path)
	// Mark as explicit watch.
	if ok {
		if fromAdd && existing.isdir {
			existing.watchingDir = true
			existing.fflags |= unix.NOTE_WRITE
			w.watches.update(existing)

			// Make sure we're watching with note_write.
			err := w.register(existing.wd, unix.EV_ADD|unix.EV_CLEAR|unix.EV_ENABLE, existing.fflags)
			if err != nil {
				return err
			}
			_, err = w.scanDir(watch{
				wd:          existing.wd,
				path:        path,
				fflags:      fflags,
				withOp:      withOp,
				isdir:       true,
				watchingDir: true,
			}, nil)
			return err
		}
		return nil
	}

	// wd, err := internal.IgnoringEINTR2(func() (*os.File, error) {
	// 	return unix.Open(path, openMode, 0)
	// })
	m := openMode
	if !fromAdd { // XXX: implement follow option
		m |= openNofollow
	}
	fp, err := os.OpenFile(path, m, 0)
	if openNofollow > 0 && err == nil { // Make sure we get EACCESS or EPERM
		var fd int
		fd, err = unix.Openat(int(fp.Fd()), path, openMode, 0)
		if err == nil {
			unix.Close(fd)
		}
	}
	if err != nil {
		if fromAdd { // Always return error from AddWith().
			return err
		}
		if errors.Is(err, unix.ENOENT) { // Doesn't exist: probably race. Ignore.
			return nil
		}

		// No permission to read the file; that's not a problem: just skip. But
		// do add it to w.fileExists to prevent it from being picked up as a
		// "new" file later (it still shows up in the directory listing).
		// ^ TODO
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
			w.watches.add(path, -1, nil, -1, false, 0, false)
			return nil
		}
		if errors.Is(err, unix.ENOTSUP) { // socket
			return nil
		}
		return err
	}
	fi, err := fp.Stat() // TODO: lstat?
	if err != nil {
		return err
	}
	switch fi.Mode().Type() {
	case fs.ModeSocket, fs.ModeCharDevice, fs.ModeNamedPipe:
		return nil
	}

	wd := int(fp.Fd())
	listDir = fi.IsDir() && listDir

	if listDir {
		// Watched directory always needs Write; otherwise we can't pick up on
		// new files.
		fflags |= unix.NOTE_WRITE
	}

	// XXX: on unreadable file this now fails with "bad file descriptor" due to
	// O_PATH.
	err = w.register(wd, unix.EV_ADD|unix.EV_CLEAR|unix.EV_ENABLE, fflags)
	if err != nil {
		unix.Close(wd)
		return err
	}
	w.watches.add(path, wd, fi, fflags, listDir, withOp, fromAdd)

	if listDir {
		_, err = w.scanDir(watch{
			wd:          wd,
			path:        path,
			fflags:      fflags,
			withOp:      withOp,
			isdir:       fi.IsDir(),
			watchingDir: true,
		}, nil)
		return err
	}
	return nil
}

func (w *kqueue) remove(watch watch) error {
	if watch.wd == -1 && watch.fflags == -1 { // "Fake" for unreadable files.
		return nil
	}
	err := w.register(watch.wd, unix.EV_DELETE, 0)
	if err != nil {
		return fmt.Errorf("unregistering %q: %w", watch.path, err)
	}
	_ = unix.Close(watch.wd)
	w.watches.remove(watch)

	if watch.isdir {
		// XXX: don't loop over all watches; should keep track of this in dir.
		for path, wd := range w.watches.path {
			// Don't remove watch if we're watching this, as that will trigger
			// its own.
			if !watch.watchingDir && filepath.Dir(path) == watch.path {
				err := w.remove(w.watches.byWd(wd))
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// newKqueue creates a new kernel event queue and returns a descriptor.
//
// This registers a new event on closepipe, which will trigger an event when
// it's closed. This way we can use kevent() without timeout/polling; without
// the closepipe, it would block forever and we wouldn't be able to stop it at
// all.
func newKqueue() (kq int, closepipe [2]int, err error) {
	kq, err = unix.Kqueue()
	if err != nil {
		return kq, closepipe, err
	}

	// Register the close pipe.
	err = unix.Pipe(closepipe[:])
	if err != nil {
		unix.Close(kq)
		return kq, closepipe, err
	}
	unix.CloseOnExec(closepipe[0])
	unix.CloseOnExec(closepipe[1])

	// Register changes to listen on the closepipe.
	changes := make([]unix.Kevent_t, 1)
	// SetKevent converts int to the platform-specific types.
	unix.SetKevent(&changes[0], closepipe[0], unix.EVFILT_READ,
		unix.EV_ADD|unix.EV_ENABLE|unix.EV_ONESHOT)

	ok, err := unix.Kevent(kq, changes, nil, nil)
	if ok == -1 {
		unix.Close(kq)
		unix.Close(closepipe[0])
		unix.Close(closepipe[1])
		return kq, closepipe, err
	}
	return kq, closepipe, nil
}

func (w *kqueue) register(wd int, flags int, fflags int) error {
	changes := []unix.Kevent_t{{Fflags: uint32(fflags)}}
	unix.SetKevent(&changes[0], wd, unix.EVFILT_VNODE, flags)
	_, err := unix.Kevent(w.kq, changes, nil, nil)
	return err
}

func (w *kqueue) state() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for wd, ww := range w.watches.wd {
		fmt.Fprintf(os.Stderr, "%4d  %q\n      isdir=%v watchingDir=%v recurse=%v fflags=0x%x\n",
			wd, ww.path, ww.isdir, ww.watchingDir, ww.recurse, ww.fflags)
	}
}
