package fsnotify

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type recursive struct {
	b         backend
	paths     map[string]withOpts
	pathsMu   sync.Mutex
	ev        chan Event
	errs      chan error
	ev_base   chan Event
	errs_base chan error
	done      chan struct{}
	doneMu    sync.Mutex
	doneResp  chan struct{}
}

func newRecursiveBackend(ev chan Event, errs chan error) (backend, error) {
	return newRecursiveBufferedBackend(0, ev, errs)
}

func newRecursiveBufferedBackend(sz uint, ev chan Event, errs chan error) (backend, error) {
	// Make base backend
	ev_base := make(chan Event)
	errs_base := make(chan error)
	b, err := newBufferedBackend(sz, ev_base, errs_base)
	if err != nil {
		return nil, err
	}

	// Wrap base backend in recursive backend
	w := &recursive{
		b:            b,
		paths:        make(map[string]withOpts),
		ev:           ev,
		errs:         errs,
		ev_base:   ev_base,
		errs_base: errs_base,
		done:         make(chan struct{}),
		doneResp:     make(chan struct{}),
	}

	// Pipe base events through the recursive backend
	go w.pipeEvents()

	return w, nil
}

func (w *recursive) getOptions(path string) (withOpts, error) {
	w.pathsMu.Lock()
	defer w.pathsMu.Unlock()
	for prefix, with := range w.paths {
		if strings.HasPrefix(path, prefix) {
			return with, nil
		}
	}
	return defaultOpts, fmt.Errorf("%w: %s", ErrNonExistentWatch, path)
}

func (w *recursive) pipeEvents() {
	defer func() {
		close(w.doneResp)
		close(w.errs)
		close(w.ev)
	}()

	for {
		select {
		case <-w.done:
			return
		case evt, ok := <-w.ev_base:
			if !ok {
				return
			}
			w.sendEvent(evt)

			if evt.Has(Create) {
				// Establish recursive watch and, if requested, send create events
				// for all children
				with, err := w.getOptions(evt.Name)
				if err == nil && with.recurse {
					first := true
					filepath.WalkDir(evt.Name, func(path string, d fs.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if d.IsDir() && runtime.GOOS != "windows" {
							return w.b.Add(path)

							// Ideally the options would be passed but no such function exists
							// at present
							//return w.b.AddWith(path, with)
						}
						if !first && with.sendCreate {	// event for first already sent above
							w.sendEvent(Event{Name: path, Op: Create})
						}
						first = false
						return nil
					})
				}
			}
		case err, ok := <-w.errs_base:
			if !ok {
				return
			}
			w.sendError(err)
		}
	}
}

// Returns true if the event was sent, or false if watcher is closed.
func (w *recursive) sendEvent(e Event) bool {
	select {
	case <-w.done:
		return false
	case w.ev <- e:
		return true
	}
}

// Returns true if the error was sent, or false if watcher is closed.
func (w *recursive) sendError(err error) bool {
	if err == nil {
		return true
	}
	select {
	case <-w.done:
		return false
	case w.errs <- err:
		return true
	}
}

func (w *recursive) isClosed() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *recursive) Close() error {
	w.doneMu.Lock()
	if w.isClosed() {
		w.doneMu.Unlock()
		return nil
	}
	err := w.b.Close()
	close(w.done)
	w.doneMu.Unlock()
	<-w.doneResp
	return err
}

func (w *recursive) Add(path string) error {
	return w.AddWith(path)
}

func (w *recursive) AddWith(path string, opts ...addOpt) error {
	base, recurse := recursivePath(path);
	with := getOptions(opts...)
	with.recurse = recurse
	w.pathsMu.Lock()
	w.paths[base] = with
	w.pathsMu.Unlock()

	if recurse {
		if runtime.GOOS == "windows" {
			// Windows backend expects the /... at the end of the path
			err := w.b.AddWith(path, opts...)
			if err != nil {
				return err
			}
		}
		if runtime.GOOS != "windows" || with.sendCreate {
			return filepath.WalkDir(base, func(root string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				// Recursively watch directories if backend does not support natively
				if d.IsDir() && runtime.GOOS != "windows" {
					err := w.b.AddWith(root, opts...)
					if err != nil {
						return err
					}
				}
				
				// Send create events for all directories and files recursively when
				// a new directory is created. This ensures that any files created
				// while the recursive watch is being established are reported as
				// created, in addition to any existing files and directories from an
				// existing directory hierarchy moved in. It includes the special case
				// of `mkdir -p one/two/three` on some systems, where only the creation
				// of `one` may be reported. More generally, it includes the case
				// `mkdir -p /tmp/one/two/three && mv /tmp/one one`, i.e. an existing
				// directory hierarchy moved in, which also only the create of `one`
				// may be reported.
				if with.sendCreate {
					w.ev <- Event{Name: root, Op: Create}
				}

				return nil
			})
		}
	} else {
		return w.b.AddWith(base, opts...)
	}
	return nil
}

func (w *recursive) Remove(path string) error {
	base, recurse := recursivePath(path);
	with, err := w.getOptions(base)
	if err != nil {
		return err
	}
	if recurse && !with.recurse {
		return fmt.Errorf("can't use /... with non-recursive watch %q", base)
	}
	w.pathsMu.Lock()
	delete(w.paths, base)
	w.pathsMu.Unlock()

	if with.recurse {
		if runtime.GOOS == "windows" {
			// Windows backend expects the /... at the end of the path
			return w.b.Remove(path)
		} else {
			// Recursively remove directories
			return filepath.WalkDir(base, func(root string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				} else if d.IsDir() {
					return w.b.Remove(root)
				} else {
					return nil
				}
			})
		}
	} else {
		return w.b.Remove(base)
	}
}

func (w *recursive) WatchList() []string {
	return w.b.WatchList()
}

func (w *recursive) xSupports(op Op) bool {
	return w.b.xSupports(op);
}
