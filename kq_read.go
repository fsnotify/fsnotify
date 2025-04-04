package fsnotify

import (
	"fmt"
	"os"
	"strings"

	"github.com/fsnotify/fsnotify/internal"
	"golang.org/x/sys/unix"
)

func (w *kqueue) readEvents() {
	defer func() {
		close(w.Events)
		close(w.Errors)
		_ = unix.Close(w.kq)
		_ = unix.Close(w.closepipe[0])
	}()

	events := make([]unix.Kevent_t, 10)
	for {
		n, err := unix.Kevent(w.kq, nil, events, nil)
		// EINTR is okay, the syscall was interrupted before timeout expired.
		if err != nil {
			if err != unix.EINTR {
				if !w.sendError(fmt.Errorf("reading events: %w", err)) {
					return
				}
			}
			continue
		}

		for _, event := range events[:n] {
			if int(event.Ident) == w.closepipe[0] {
				return
			}

			ev, ok := w.handleEvent(event)
			if !ok {
				return
			}
			for _, e := range ev {
				if !w.sendEvent(e) {
					return
				}
			}
		}
	}
}

func (w *kqueue) handleEvent(event unix.Kevent_t) ([]Event, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var (
		watch  = w.watches.byWd(int(event.Ident))
		ev     = w.newEvent(watch.path, int(event.Fflags))
		events = make([]Event, 1, 4)
	)
	events[0] = ev

	if debug {
		internal.Debug(watch.path, &event)
	}

	// Stop watching on remove.
	if ev.Has(Remove) {
		err := w.remove(watch)
		if !w.sendError(err) {
			return nil, false
		}
		// Look for a file that may have overwritten this; for example, mv f1 f2
		// will delete f2, then create f2.
		if !watch.byUser && watch.withOp.Has(Create) {
			_, err := os.Lstat(watch.path)
			if err == nil {
				w.addWatch(watch.path, watch.fflags, watch.withOp, watch.watchingDir, false)
				events = append(events, Event{Name: watch.path, Op: Create})
			}
		}

		if !watch.withOp.Has(Remove) {
			return nil, true
		}
	}

	// Stop watching on rename.
	if ev.Has(Rename) {
		err := w.remove(watch)
		if !w.sendError(err) {
			return nil, false
		}
		if !watch.withOp.Has(Rename) {
			return nil, true
		}
	}

	// kqueue sends write every time a file inside a dir changes. Don't send
	// that event, but do need to scan all files, add watches for them, and send
	// a Create event.
	if watch.watchingDir && ev.Has(Write) {
		var err error
		events, err = w.scanDir(watch, events)
		if !w.sendError(err) {
			return nil, false
		}
		events = events[1:]
	}

	return events, true
}

func (w *kqueue) scanDir(watch watch, events []Event) ([]Event, error) {
	ls, err := os.ReadDir(watch.path)
	if err != nil {
		return events, fmt.Errorf("scanDir: %w", err)
	}

	for _, f := range ls {
		path := watch.path + "/" + f.Name()
		if _, ok := w.watches.byPath(path); !ok {
			fflags := watch.fflags
			if !watch.recurse && f.IsDir() { // Don't add write for subdirs.
				fflags &^= unix.NOTE_WRITE
			}
			err := w.addWatch(path, fflags, watch.withOp, watch.recurse, false)
			if err != nil {
				if !strings.Contains(err.Error(), path) {
					err = fmt.Errorf("%q: %w", path, err)
				}
				return events, fmt.Errorf("scanDir: %w", err)
			}
			if events != nil && watch.withOp.Has(Create) {
				events = append(events, Event{Name: path, Op: Create})
			}
		}
	}
	return events, nil
}

func (w *kqueue) newEvent(path string, fflags int) Event {
	e := Event{Name: path}
	// if linkName != "" {
	// 	// If the user watched "/path/link" then emit events as "/path/link"
	// 	// rather than "/path/target".
	// 	e.Name = linkName
	// }

	if fflags&unix.NOTE_DELETE == unix.NOTE_DELETE {
		e.Op |= Remove
	}
	if fflags&unix.NOTE_WRITE == unix.NOTE_WRITE {
		e.Op |= Write
	}
	if fflags&unix.NOTE_RENAME == unix.NOTE_RENAME {
		e.Op |= Rename
	}
	if fflags&unix.NOTE_ATTRIB == unix.NOTE_ATTRIB {
		e.Op |= Chmod
	}
	if fflags&unix.NOTE_OPEN == unix.NOTE_OPEN {
		e.Op |= xUnportableOpen
	}
	if fflags&unix.NOTE_READ == unix.NOTE_READ {
		e.Op |= xUnportableRead
	}
	if fflags&unix.NOTE_CLOSE_WRITE == unix.NOTE_CLOSE_WRITE {
		e.Op |= xUnportableCloseWrite
	}
	// No point sending a write and delete event at the same time: if it's gone,
	// then it's gone.
	if e.Op.Has(Write) && e.Op.Has(Remove) {
		e.Op &^= Write
	}
	return e
}
