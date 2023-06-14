//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrCapSysAdmin indicates caller is missing CAP_SYS_ADMIN permissions
	ErrCapSysAdmin = errors.New("require CAP_SYS_ADMIN capability")
	// ErrInvalidFlagValue indicates flag value is invalid
	ErrInvalidFlagValue = errors.New("invalid flag value")
	// ErrMountPoint indicates the path is not under watched mount point
	ErrMountPoint = errors.New("path not under watched mount point")
)

// Watcher watches a set of paths, delivering events on a channel.
//
// A watcher should not be copied (e.g. pass it by pointer, rather than by
// value).
type Watcher struct {
	// Events sends the filesystem change events.
	//
	// fsnotify can send the following events; a "path" here can refer to a
	// file, directory, symbolic link, or special file like a FIFO.
	//
	//   fsnotify.Create              A new path was created; this may be followed by one
	//                                or more Write events if data also gets written to a
	//                                file.
	//
	//   fsnotify.Remove              A path was removed.
	//
	//   fsnotify.Rename              A path was renamed. A rename is always sent with the
	//                                old path as Event.Name, and a Create event will be
	//                                sent with the new name. Renames are only sent for
	//                                paths that are currently watched; e.g. moving an
	//                                unmonitored file into a monitored directory will
	//                                show up as just a Create. Similarly, renaming a file
	//                                to outside a monitored directory will show up as
	//                                only a Rename.
	//
	//   fsnotify.Write               A file or named pipe was written to. A Truncate will
	//                                also trigger a Write. A single "write action"
	//                                initiated by the user may show up as one or multiple
	//                                writes, depending on when the system syncs things to
	//                                disk. For example when compiling a large Go program
	//                                you may get hundreds of Write events, so you
	//                                probably want to wait until you've stopped receiving
	//                                them (see the dedup example in cmd/fsnotify).
	//                                Some systems may send Write event for directories
	//                                when the directory content changes.
	//
	//   fsnotify.Chmod               Attributes were changed. On Linux this is also sent
	//                                when a file is removed (or more accurately, when a
	//                                link to an inode is removed). On kqueue it's sent
	//                                and on kqueue when a file is truncated. On Windows
	//                                it's never sent.
	//
	//   fsnotify.Read                File or directory was read. (Applicable only to fanotify watcher.)
	//
	//   fsnotify.Close               File was closed without a write. (Applicable only to fanotify watcher.)
	//
	//   fsnotify.Open                File or directory was opened. (Applicable only to fanotify watcher.)
	//
	//   fsnotify.Execute             File was opened for execution. (Applicable only to fanotify watcher.)
	Events chan FanotifyEvent

	// PermissionEvents holds permission request events for the watched file/directory.
	//   fsnotify.PermissionToOpen    Permission request to open a file or directory. (Applicable only to fanotify watcher.)
	//   fsnotify.PermissionToExecute Permission to open file for execution. (Applicable only to fanotify watcher.)
	//   fsnotify.PermissionToRead    Permission to read a file or directory. (Applicable only to fanotify watcher.)
	PermissionEvents chan FanotifyEvent

	// Errors sends any errors.
	Errors chan error

	fd                 int
	flags              uint   // flags passed to fanotify_init
	markMask           uint64 // fanotify_mark mask
	mountPointFile     *os.File
	mountDeviceID      uint64
	findMountPoint     sync.Once
	closeOnce          sync.Once
	isClosed           bool
	kernelMajorVersion int
	kernelMinorVersion int
	done               chan struct{}
	stopper            struct {
		r *os.File
		w *os.File
	}
	isFanotify bool
}

// FanotifyEvent represents a notification or a permission event from the kernel for the file,
// directory marked for watching.
// Notification events are merely informative and require
// no action to be taken by the receiving application with the exception being that the
// file descriptor provided within the event must be closed.
// Permission events are requests to the receiving application to decide whether permission
// for a file access shall be granted. For these events, the recipient must write a
// response which decides whether access is granted or not.
type FanotifyEvent struct {
	Event
	// Fd is the open file descriptor for the file/directory being watched
	Fd int
	// Pid Process ID of the process that caused the event
	Pid int
}

// NewWatcher returns a fanotify watcher from which filesystem
// notification events can be read. Each watcher
// supports watching for events under a single mount point.
// For cases where multiple mount points need to be monitored
// multiple watcher instances need to be used.
//
// Notification events are merely informative and require
// no action to be taken by the receiving application with the
// exception being that the file descriptor provided within the
// event must be closed.
//
// The function returns a new instance of the watcher. The fanotify flags
// are set based on the running kernel version. [ErrCapSysAdmin] is returned
// if the process does not have CAP_SYS_ADM capability.
//
//  - For Linux kernel version 5.0 and earlier no additional information about
//    the underlying filesystem object is available.
//  - For Linux kernel versions 5.1 till 5.8 (inclusive) additional information
//    about the underlying filesystem object is correlated to an event.
//  - For Linux kernel version 5.9 or later the modified file name is made available
//    in the event.
func NewWatcher() (*Watcher, error) {
	capSysAdmin, err := checkCapSysAdmin()
	if err != nil {
		return nil, err
	}
	if !capSysAdmin {
		return nil, ErrCapSysAdmin
	}
	w, err := newFanotifyWatcher()
	if err != nil {
		return nil, err
	}
	go w.start()
	return w, nil
}

// Add starts monitoring the path for changes.
//
// A path can only be watched once; attempting to watch it more than once will
// return an error. Paths that do not yet exist on the filesystem cannot be
// watched.
//
// Returns [ErrClosed] if [Watcher.Close] was called.
//
// See [AddWith] for a version that allows adding options.
//
// # Watching directories
//
// All files in a directory are monitored, including new files that are created
// after the watcher is started. By default subdirectories are not watched (i.e.
// it's non-recursive).
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
// you're not interested in.
func (w *Watcher) Add(name string) error { return w.AddWith(name) }

// AddWith is like [Add], but allows adding options.
func (w *Watcher) AddWith(name string, opts ...addOpt) error {
	if w.isClosed {
		return ErrClosed
	}
	name = filepath.Clean(name)
	_ = getOptions(opts...)
	return w.fanotifyAddPath(name)
}

// Remove stops monitoring the path for changes.
//
// Returns nil if [Watcher.Close] was called.
func (w *Watcher) Remove(name string) error {
	if w.isClosed {
		return nil
	}
	name = filepath.Clean(name)
	return w.fanotifyRemove(name)
}

// WatchList returns all paths added with [Add] (and are not yet removed).
//
// Returns nil if [Watcher.Close] was called.
func (w *Watcher) WatchList() []string {
	if w.isClosed {
		return nil
	}
	return nil
}

// Close stops the watcher and closes the event channels
func (w *Watcher) Close() error {
	w.closeFanotify()
	return nil
}
