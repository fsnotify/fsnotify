// Package fsnotify provides a cross-platform interface for file system
// notifications.
package fsnotify

import (
	"errors"
	"fmt"
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
	Create Op = 1 << iota
	Write
	Remove
	Rename
	Chmod

	// All supported events.
	All = Create | Write | Remove | Rename | Chmod
)

// Common errors that can be reported by a watcher
var (
	ErrNonExistentWatch = errors.New("fsnotify: can't remove non-existent watcher")
	ErrClosed           = errors.New("fsnotify: watcher already closed")
	ErrEventOverflow    = errors.New("fsnotify: queue or buffer overflow")
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
		events  Op
	}
)

var defaultOpts = withOpts{
	bufsize: 65536, // 64K
	events:  Create | Write | Remove | Rename | Chmod,
}

func getOptions(opts ...addOpt) (withOpts, error) {
	with := defaultOpts
	for _, o := range opts {
		o(&with)
	}

	if with.bufsize < 4096 {
		return with, fmt.Errorf("fsnotify.WithBufferSize: buffer size cannot be smaller than 4096 bytes")
	}
	if with.events == 0 {
		return with, fmt.Errorf("fsnotify.WithEvents: events is 0")
	}
	if m := with.events & All; m == 0 {
		return with, fmt.Errorf("fsnotify.WithEvents: events contains unknown values: %x", m)
	}

	return with, nil
}

// WithBufferSize sets the buffer size for the Windows backend. This is a no-op
// for other backends.
//
// The default value is 64K (65536 bytes) which should be enough for most
// applications, but you can increase it if you're hitting "queue or buffer
// overflow" errors ([ErrEventOverflow]).
func WithBufferSize(bytes int) addOpt {
	return func(opt *withOpts) { opt.bufsize = bytes }
}

// WithEvents controls which events to monitor.
//
// The default is Create | Write | Remove | Rename | Chmod.
func WithEvents(op Op) addOpt {
	return func(opt *withOpts) { opt.events = op }
}
