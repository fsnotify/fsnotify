//go:build linux && !appengine

// Note: the documentation on the Watcher type and methods is generated from
// mkdoc.zsh

package fsnotify

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify/internal"
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
// # Windows notes
//
// Paths can be added as "C:\path\to\dir", but forward slashes
// ("C:/path/to/dir") will also work.
//
// When a watched directory is removed it will always send an event for the
// directory itself, but may not send events for all files in that directory.
// Sometimes it will send events for all times, sometimes it will send no
// events, and often only for some files.
//
// The default ReadDirectoryChangesW() buffer size is 64K, which is the largest
// value that is guaranteed to work with SMB filesystems. If you have many
// events in quick succession this may not be enough, and you will have to use
// [WithBufferSize] to increase the value.
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
	//                      you may get hundreds of Write events, and you may
	//                      want to wait until you've stopped receiving them
	//                      (see the dedup example in cmd/fsnotify).
	//
	//                      Some systems may send Write event for directories
	//                      when the directory content changes.
	//
	//   fsnotify.Chmod     Attributes were changed. On Linux this is also sent
	//                      when a file is removed (or more accurately, when a
	//                      link to an inode is removed). On kqueue it's sent
	//                      when a file is truncated. On Windows it's never
	//                      sent.
	Events chan Event

	// Errors sends any errors.
	Errors chan error

	// Store fd here as os.File.Read() will no longer return on close after
	// calling Fd(). See: https://github.com/golang/go/issues/26439
	fd          int
	inotifyFile *os.File
	watches     *watches
	done        chan struct{} // Channel for sending a "quit message" to the reader goroutine
	doneMu      sync.Mutex
	doneResp    chan struct{} // Channel to respond to Close

	// Store rename cookies in an array, with the index wrapping to 0. Almost
	// all of the time what we get is a MOVED_FROM to set the cookie and the
	// next event inotify sends will be MOVED_TO to read it. However, this is
	// not guaranteed – as described in inotify(7) – and we may get other events
	// between the two MOVED_* events (including other MOVED_* ones).
	//
	// A second issue is that moving a file outside the watched directory will
	// trigger a MOVED_FROM to set the cookie, but we never see the MOVED_TO to
	// read and delete it. So just storing it in a map would slowly leak memory.
	//
	// Doing it like this gives us a simple fast LRU-cache that won't allocate.
	// Ten items should be more than enough for our purpose, and a loop over
	// such a short array is faster than a map access anyway (not that it hugely
	// matters since we're talking about hundreds of ns at the most, but still).
	cookies     [10]koekje
	cookieIndex uint8
	cookiesMu   sync.Mutex
}

type (
	watches struct {
		mu   sync.RWMutex
		wd   map[uint32]*watch // wd → watch
		path map[string]uint32 // pathname → wd
	}
	watch struct {
		wd      uint32 // Watch descriptor (as returned by the inotify_add_watch() syscall)
		flags   uint32 // inotify flags of this watch (see inotify(7) for the list of valid flags)
		path    string // Watch path.
		recurse bool   // Recursion with ./...?
	}
	koekje struct {
		cookie uint32
		path   string
	}
)

func newWatches() *watches {
	return &watches{
		wd:   make(map[uint32]*watch),
		path: make(map[string]uint32),
	}
}

func (w *watches) len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.wd)
}

func (w *watches) add(ww *watch) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wd[ww.wd] = ww
	w.path[ww.path] = ww.wd
}

func (w *watches) remove(wd uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.path, w.wd[wd].path)
	delete(w.wd, wd)
}

func (w *watches) removePath(path string) ([]uint32, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	path, recurse := recursivePath(path)
	wd, ok := w.path[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNonExistentWatch, path)
	}

	watch := w.wd[wd]
	if recurse && !watch.recurse {
		return nil, fmt.Errorf("can't use /... with non-recursive watch %q", path)
	}

	delete(w.path, path)
	delete(w.wd, wd)
	if !watch.recurse {
		return []uint32{wd}, nil
	}

	wds := make([]uint32, 0, 8)
	wds = append(wds, wd)
	for p, rwd := range w.path {
		if filepath.HasPrefix(p, path) {
			delete(w.path, p)
			delete(w.wd, rwd)
			wds = append(wds, rwd)
		}
	}
	return wds, nil
}

func (w *watches) byPath(path string) *watch {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.wd[w.path[path]]
}

func (w *watches) byWd(wd uint32) *watch {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.wd[wd]
}

func (w *watches) updatePath(path string, f func(*watch) (*watch, error)) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var existing *watch
	wd, ok := w.path[path]
	if ok {
		existing = w.wd[wd]
	}

	upd, err := f(existing)
	if err != nil {
		return err
	}
	if upd != nil {
		w.wd[upd.wd] = upd
		w.path[upd.path] = upd.wd

		if upd.wd != wd {
			delete(w.wd, wd)
		}
	}

	return nil
}

// NewWatcher creates a new Watcher.
func NewWatcher() (*Watcher, error) {
	return NewBufferedWatcher(0)
}

// NewBufferedWatcher creates a new Watcher with a buffered Watcher.Events
// channel.
//
// The main use case for this is situations with a very large number of events
// where the kernel buffer size can't be increased (e.g. due to lack of
// permissions). An unbuffered Watcher will perform better for almost all use
// cases, and whenever possible you will be better off increasing the kernel
// buffers instead of adding a large userspace buffer.
func NewBufferedWatcher(sz uint) (*Watcher, error) {
	// Need to set nonblocking mode for SetDeadline to work, otherwise blocking
	// I/O operations won't terminate on close.
	fd, errno := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if fd == -1 {
		return nil, errno
	}

	w := &Watcher{
		fd:          fd,
		inotifyFile: os.NewFile(uintptr(fd), ""),
		watches:     newWatches(),
		Events:      make(chan Event, sz),
		Errors:      make(chan error),
		done:        make(chan struct{}),
		doneResp:    make(chan struct{}),
	}

	go w.readEvents()
	return w, nil
}

// Returns true if the event was sent, or false if watcher is closed.
func (w *Watcher) sendEvent(e Event) bool {
	select {
	case <-w.done:
		return false
	case w.Events <- e:
		return true
	}
}

// Returns true if the error was sent, or false if watcher is closed.
func (w *Watcher) sendError(err error) bool {
	if err == nil {
		return true
	}
	select {
	case <-w.done:
		return false
	case w.Errors <- err:
		return true
	}
}

func (w *Watcher) isClosed() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// Close removes all watches and closes the Events channel.
func (w *Watcher) Close() error {
	w.doneMu.Lock()
	if w.isClosed() {
		w.doneMu.Unlock()
		return nil
	}
	close(w.done)
	w.doneMu.Unlock()

	// Causes any blocking reads to return with an error, provided the file
	// still supports deadline operations.
	err := w.inotifyFile.Close()
	if err != nil {
		return err
	}

	// Wait for goroutine to close
	<-w.doneResp

	return nil
}

// Add starts monitoring the path for changes.
//
// A path can only be watched once; watching it more than once is a no-op and will
// not return an error. Paths that do not yet exist on the filesystem cannot be
// watched.
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
// See [Watcher.AddWith] for a version that allows adding options.
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
// recommended as many programs (especially editors) update files atomically: it
// will write to a temporary file which is then moved to to destination,
// overwriting the original (or some variant thereof). The watcher on the
// original file is now lost, as that no longer exists.
//
// The upshot of this is that a power failure or crash won't leave a
// half-written file.
//
// Watch the parent directory and use Event.Name to filter out files you're not
// interested in. There is an example of this in cmd/fsnotify/file.go.
func (w *Watcher) Add(name string) error { return w.AddWith(name) }

// AddWith is like [Watcher.Add], but allows adding options. When using Add()
// the defaults described below are used.
//
// Possible options are:
//
//   - [WithBufferSize] sets the buffer size for the Windows backend; no-op on
//     other platforms. The default is 64K (65536 bytes).
func (w *Watcher) AddWith(path string, opts ...addOpt) error {
	if w.isClosed() {
		return ErrClosed
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), path)
	}

	with := getOptions(opts...)
	if !w.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}

	path, recurse := recursivePath(path)
	if recurse {
		return filepath.WalkDir(path, func(root string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if root == path {
					return fmt.Errorf("fsnotify: not a directory: %q", path)
				}
				return nil
			}

			// Send a Create event when adding new directory from a recursive
			// watch; this is for "mkdir -p one/two/three". Usually all those
			// directories will be created before we can set up watchers on the
			// subdirectories, so only "one" would be sent as a Create event and
			// not "one/two" and "one/two/three" (inotifywait -r has the same
			// problem).
			if with.sendCreate && root != path {
				w.sendEvent(Event{Name: root, Op: Create})
			}

			return w.add(root, with, true)
		})
	}

	return w.add(path, with, false)
}

func (w *Watcher) add(path string, with withOpts, recurse bool) error {
	var flags uint32
	if with.noFollow {
		flags |= unix.IN_DONT_FOLLOW
	}
	if with.op.Has(Create) {
		flags |= unix.IN_CREATE
	}
	if with.op.Has(Write) {
		flags |= unix.IN_MODIFY
	}
	if with.op.Has(Remove) {
		flags |= unix.IN_DELETE | unix.IN_DELETE_SELF
	}
	if with.op.Has(Rename) {
		flags |= unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_MOVE_SELF
	}
	if with.op.Has(Chmod) {
		flags |= unix.IN_ATTRIB
	}
	if with.op.Has(xUnportableOpen) {
		flags |= unix.IN_OPEN
	}
	if with.op.Has(xUnportableRead) {
		flags |= unix.IN_ACCESS
	}
	if with.op.Has(xUnportableCloseWrite) {
		flags |= unix.IN_CLOSE_WRITE
	}
	if with.op.Has(xUnportableCloseRead) {
		flags |= unix.IN_CLOSE_NOWRITE
	}
	return w.register(path, flags, recurse)
}

func (w *Watcher) register(path string, flags uint32, recurse bool) error {
	return w.watches.updatePath(path, func(existing *watch) (*watch, error) {
		if existing != nil {
			flags |= existing.flags | unix.IN_MASK_ADD
		}

		wd, err := unix.InotifyAddWatch(w.fd, path, flags)
		if wd == -1 {
			return nil, err
		}

		if existing == nil {
			return &watch{
				wd:      uint32(wd),
				path:    path,
				flags:   flags,
				recurse: recurse,
			}, nil
		}

		existing.wd = uint32(wd)
		existing.flags = flags
		return existing, nil
	})
}

// Remove stops monitoring the path for changes.
//
// Directories are always removed non-recursively. For example, if you added
// /tmp/dir and /tmp/dir/subdir then you will need to remove both.
//
// Removing a path that has not yet been added returns [ErrNonExistentWatch].
//
// Returns nil if [Watcher.Close] was called.
func (w *Watcher) Remove(name string) error {
	if w.isClosed() {
		return nil
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), name)
	}
	return w.remove(filepath.Clean(name))
}

func (w *Watcher) remove(name string) error {
	wds, err := w.watches.removePath(name)
	if err != nil {
		return err
	}

	for _, wd := range wds {
		_, err := unix.InotifyRmWatch(w.fd, wd)
		if err != nil {
			// TODO: Perhaps it's not helpful to return an error here in every
			// case; the only two possible errors are:
			//
			// EBADF, which happens when w.fd is not a valid file descriptor of
			// any kind.
			//
			// EINVAL, which is when fd is not an inotify descriptor or wd is
			// not a valid watch descriptor. Watch descriptors are invalidated
			// when they are removed explicitly or implicitly; explicitly by
			// inotify_rm_watch, implicitly when the file they are watching is
			// deleted.
			return err
		}
	}
	return nil
}

// WatchList returns all paths explicitly added with [Watcher.Add] (and are not
// yet removed).
//
// Returns nil if [Watcher.Close] was called.
func (w *Watcher) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	entries := make([]string, 0, w.watches.len())
	w.watches.mu.RLock()
	for pathname := range w.watches.path {
		entries = append(entries, pathname)
	}
	w.watches.mu.RUnlock()

	return entries
}

// readEvents reads from the inotify file descriptor, converts the
// received events into Event objects and sends them via the Events channel
func (w *Watcher) readEvents() {
	defer func() {
		close(w.doneResp)
		close(w.Errors)
		close(w.Events)
	}()

	var (
		buf   [unix.SizeofInotifyEvent * 4096]byte // Buffer for a maximum of 4096 raw events
		errno error                                // Syscall errno
	)
	for {
		// See if we have been closed.
		if w.isClosed() {
			return
		}

		n, err := w.inotifyFile.Read(buf[:])
		switch {
		case errors.Unwrap(err) == os.ErrClosed:
			return
		case err != nil:
			if !w.sendError(err) {
				return
			}
			continue
		}

		if n < unix.SizeofInotifyEvent {
			var err error
			if n == 0 {
				err = io.EOF // If EOF is received. This should really never happen.
			} else if n < 0 {
				err = errno // If an error occurred while reading.
			} else {
				err = errors.New("notify: short read in readEvents()") // Read was too short.
			}
			if !w.sendError(err) {
				return
			}
			continue
		}

		// We don't know how many events we just read into the buffer
		// While the offset points to at least one whole event...
		var offset uint32
		for offset <= uint32(n-unix.SizeofInotifyEvent) {
			var (
				// Point "raw" to the event in the buffer
				raw     = (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))
				mask    = uint32(raw.Mask)
				nameLen = uint32(raw.Len)
				// Move to the next event in the buffer
				next = func() { offset += unix.SizeofInotifyEvent + nameLen }
			)

			if mask&unix.IN_Q_OVERFLOW != 0 {
				if !w.sendError(ErrEventOverflow) {
					return
				}
			}

			// If the event happened to the watched directory or the watched file, the kernel
			// doesn't append the filename to the event, but we would like to always fill the
			// the "Name" field with a valid filename. We retrieve the path of the watch from
			// the "paths" map.
			watch := w.watches.byWd(uint32(raw.Wd))

			var name string
			if watch != nil {
				name = watch.path
			}
			if nameLen > 0 {
				// Point "bytes" at the first byte of the filename
				bytes := (*[unix.PathMax]byte)(unsafe.Pointer(&buf[offset+unix.SizeofInotifyEvent]))[:nameLen:nameLen]
				// The filename is padded with NULL bytes. TrimRight() gets rid of those.
				name += "/" + strings.TrimRight(string(bytes[0:nameLen]), "\000")
			}

			if debug {
				internal.Debug(name, raw.Mask, raw.Cookie)
			}

			if mask&unix.IN_IGNORED != 0 { //&& event.Op != 0
				next()
				continue
			}

			// inotify will automatically remove the watch on deletes; just need
			// to clean our state here.
			if watch != nil && mask&unix.IN_DELETE_SELF == unix.IN_DELETE_SELF {
				w.watches.remove(watch.wd)
			}

			// We can't really update the state when a watched path is moved;
			// only IN_MOVE_SELF is sent and not IN_MOVED_{FROM,TO}. So remove
			// the watch.
			if watch != nil && mask&unix.IN_MOVE_SELF == unix.IN_MOVE_SELF {
				if watch.recurse {
					next() // Do nothing
					continue
				}

				err := w.remove(watch.path)
				if err != nil && !errors.Is(err, ErrNonExistentWatch) {
					if !w.sendError(err) {
						return
					}
				}
			}

			/// Skip if we're watching both this path and the parent; the parent
			/// will already send a delete so no need to do it twice.
			if mask&unix.IN_DELETE_SELF != 0 {
				if _, ok := w.watches.path[filepath.Dir(watch.path)]; ok {
					next()
					continue
				}
			}

			ev := w.newEvent(name, mask, raw.Cookie)
			// Need to update watch path for recurse.
			if watch != nil && watch.recurse {
				isDir := mask&unix.IN_ISDIR == unix.IN_ISDIR
				/// New directory created: set up watch on it.
				if isDir && ev.Has(Create) {
					err := w.register(ev.Name, watch.flags, true)
					if !w.sendError(err) {
						return
					}

					// This was a directory rename, so we need to update all
					// the children.
					//
					// TODO: this is of course pretty slow; we should use a
					// better data structure for storing all of this, e.g. store
					// children in the watch. I have some code for this in my
					// kqueue refactor we can use in the future. For now I'm
					// okay with this as it's not publicly available.
					// Correctness first, performance second.
					if ev.renamedFrom != "" {
						w.watches.mu.Lock()
						for k, ww := range w.watches.wd {
							if k == watch.wd || ww.path == ev.Name {
								continue
							}
							if strings.HasPrefix(ww.path, ev.renamedFrom) {
								ww.path = strings.Replace(ww.path, ev.renamedFrom, ev.Name, 1)
								w.watches.wd[k] = ww
							}
						}
						w.watches.mu.Unlock()
					}
				}
			}

			/// Send the events that are not ignored on the events channel
			if !w.sendEvent(ev) {
				return
			}
			next()
		}
	}
}

func (w *Watcher) isRecursive(path string) bool {
	ww := w.watches.byPath(path)
	if ww == nil { // path could be a file, so also check the Dir.
		ww = w.watches.byPath(filepath.Dir(path))
	}
	return ww != nil && ww.recurse
}

func (w *Watcher) newEvent(name string, mask, cookie uint32) Event {
	e := Event{Name: name}
	if mask&unix.IN_CREATE == unix.IN_CREATE || mask&unix.IN_MOVED_TO == unix.IN_MOVED_TO {
		e.Op |= Create
	}
	if mask&unix.IN_DELETE_SELF == unix.IN_DELETE_SELF || mask&unix.IN_DELETE == unix.IN_DELETE {
		e.Op |= Remove
	}
	if mask&unix.IN_MODIFY == unix.IN_MODIFY {
		e.Op |= Write
	}
	if mask&unix.IN_OPEN == unix.IN_OPEN {
		e.Op |= xUnportableOpen
	}
	if mask&unix.IN_ACCESS == unix.IN_ACCESS {
		e.Op |= xUnportableRead
	}
	if mask&unix.IN_CLOSE_WRITE == unix.IN_CLOSE_WRITE {
		e.Op |= xUnportableCloseWrite
	}
	if mask&unix.IN_CLOSE_NOWRITE == unix.IN_CLOSE_NOWRITE {
		e.Op |= xUnportableCloseRead
	}
	if mask&unix.IN_MOVE_SELF == unix.IN_MOVE_SELF || mask&unix.IN_MOVED_FROM == unix.IN_MOVED_FROM {
		e.Op |= Rename
	}
	if mask&unix.IN_ATTRIB == unix.IN_ATTRIB {
		e.Op |= Chmod
	}

	if cookie != 0 {
		if mask&unix.IN_MOVED_FROM == unix.IN_MOVED_FROM {
			w.cookiesMu.Lock()
			w.cookies[w.cookieIndex] = koekje{cookie: cookie, path: e.Name}
			w.cookieIndex++
			if w.cookieIndex > 9 {
				w.cookieIndex = 0
			}
			w.cookiesMu.Unlock()
		} else if mask&unix.IN_MOVED_TO == unix.IN_MOVED_TO {
			w.cookiesMu.Lock()
			var prev string
			for _, c := range w.cookies {
				if c.cookie == cookie {
					prev = c.path
					break
				}
			}
			w.cookiesMu.Unlock()
			e.renamedFrom = prev
		}
	}
	return e
}

// Supports reports if all the listed operations are supported by this platform.
//
// Create, Write, Remove, Rename, and Chmod are always supported. It can only
// return false for an Op starting with Unportable.
func (w *Watcher) xSupports(op Op) bool {
	return true // Supports everything.
}

func (w *Watcher) state() {
	w.watches.mu.Lock()
	defer w.watches.mu.Unlock()
	for wd, ww := range w.watches.wd {
		fmt.Fprintf(os.Stderr, "%4d: recurse=%t %q\n", wd, ww.recurse, ww.path)
	}
}
