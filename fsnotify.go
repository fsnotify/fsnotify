// Package fsnotify provides a cross-platform interface for file system
// notifications.
//
// Currently supported systems:
//
//	Linux 2.6.32+    via inotify
//	BSD, macOS       via kqueue
//	Windows          via ReadDirectoryChangesW
//	illumos          via FEN
package fsnotify

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Event represents a file system notification.
type Event struct {
	// Path to the file or directory.
	//
	// Paths are relative to the input; for example with Add("dir") the Name
	// will be set to "dir/file" if you create that file, but if you use
	// Add("/path/to/dir") it will be "/path/to/dir/file".
	Name string

	// File operation that triggered the event.
	//
	// This is a bitmask and some systems may send multiple operations at once.
	// Use the Event.Has() method instead of comparing with ==.
	Op Op
}

// Op describes a set of file operations.
type Op uint32

// The operations fsnotify can trigger; see the documentation on [Watcher] for a
// full description, and check them with [Event.Has].
const (
	// A new pathname was created.
	Create Op = 1 << iota

	// The pathname was written to; this does *not* mean the write has finished,
	// and a write can be followed by more writes.
	Write

	// The path was removed; any watches on it will be removed. Some "remove"
	// operations may trigger a Rename if the file is actually moved (for
	// example "remove to trash" is often a rename).
	Remove

	// The path was renamed to something else; any watched on it will be
	// removed.
	Rename

	// File attributes were changed.
	//
	// It's generally not recommended to take action on this event, as it may
	// get triggered very frequently by some software. For example, Spotlight
	// indexing on macOS, anti-virus software, backup software, etc.
	Chmod
)

// Common errors that can be reported.
var (
	ErrNonExistentWatch = errors.New("fsnotify: can't remove non-existent watcher")
	ErrEventOverflow    = errors.New("fsnotify: queue or buffer overflow")
	ErrClosed           = errors.New("fsnotify: watcher already closed")
)

func (o Op) String() string {
	var b strings.Builder
	if o.Has(Create) {
		b.WriteString("|CREATE")
	}
	if o.Has(Remove) {
		b.WriteString("|REMOVE")
	}
	if o.Has(Write) {
		b.WriteString("|WRITE")
	}
	if o.Has(Rename) {
		b.WriteString("|RENAME")
	}
	if o.Has(Chmod) {
		b.WriteString("|CHMOD")
	}
	if b.Len() == 0 {
		return "[no events]"
	}
	return b.String()[1:]
}

// Has reports if this operation has the given operation.
func (o Op) Has(h Op) bool { return o&h == h }

// Has reports if this event has the given operation.
func (e Event) Has(op Op) bool { return e.Op.Has(op) }

// String returns a string representation of the event with their path.
func (e Event) String() string {
	return fmt.Sprintf("%-13s %q", e.Op.String(), e.Name)
}

type (
	addOpt   func(opt *withOpts)
	withOpts struct {
		bufsize int
	}
)

var defaultOpts = withOpts{
	bufsize: 65536, // 64K
}

func getOptions(opts ...addOpt) withOpts {
	with := defaultOpts
	for _, o := range opts {
		o(&with)
	}
	return with
}

// WithBufferSize sets the buffer size for the Windows backend. This is a no-op
// for other backends.
//
// The default value is 64K (65536 bytes) which is the highest value that works
// on all filesystems and should be enough for most applications, but if you
// have a large burst of events it may not be enough. You can increase it if
// you're hitting "queue or buffer overflow" errors ([ErrEventOverflow]).
func WithBufferSize(bytes int) addOpt {
	return func(opt *withOpts) { opt.bufsize = bytes }
}

// Check if this path is recursive (ends with "/..." or "\..."), and return the
// path with the /... stripped.
func recursivePath(path string) (string, bool) {
	if filepath.Base(path) == "..." {
		return filepath.Dir(path), true
	}
	return path, false
}

type newOpt func(opt *newOpts) //option function to be passed to the NewWatcher

// newOpts is used to configure some optional items in the Watcher structure during configuration.
// The newOpts structure is extensible so that new optional configuration items can be added later
type newOpts struct {
	eventbuffer uint
}

var defaultNewOpts = newOpts{
	eventbuffer: 0, // Default to an unbuffered channel
}

// WithBufferedEventChannel configures the Watcher with an optionally buffered Event channel
//
// The default Event channel is unbuffered, which means that the host kernel is solely
// responsible for buffering events.  A buffered Event channel allows for a userspace
// application to add additional event buffering without modifying kernel parameters.
// Adjusting the kernel buffers will always provide better performance on highly loaded
// systems, but a user space buffer can potentially provide some resiliency when it is
// not possible to change the kernel parameters
func WithBufferedEventChannel(sz uint) newOpt {
	return func(opt *newOpts) {
		if opt != nil {
			opt.eventbuffer = sz
		}
	}
}

// getNewOptions is just a little helper function simplify the usage of options in NewWatcher
func getNewOptions(opts ...newOpt) (r newOpts) {
	r = defaultNewOpts
	for _, o := range opts {
		o(&r)
	}
	return
}

// eventChannel creates a new Event channel based on eventbuffer value.
// A value of zero indicates an un buffered channel, values > 0 createa buffered channel.
func (o newOpts) eventChannel() chan Event {
	if o.eventbuffer == 0 {
		return make(chan Event)
	}
	return make(chan Event, o.eventbuffer)
}
