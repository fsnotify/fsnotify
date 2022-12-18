//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

var (
	// ErrCapSysAdmin indicates caller is missing CAP_SYS_ADMIN permissions
	ErrCapSysAdmin = errors.New("require CAP_SYS_ADMIN capability")
	// ErrInvalidFlagCombination indicates the bit/combination of flags are invalid
	ErrInvalidFlagCombination = errors.New("invalid flag bitmask")
	// ErrUnsupportedOnKernelVersion indicates the feature/flag is unavailable for the current kernel version
	ErrUnsupportedOnKernelVersion = errors.New("feature unsupported on current kernel version")
	// ErrWatchPath indicates path needs to be specified for watching
	ErrWatchPath = errors.New("missing watch path")
)

// EventType represents an event / operation on a particular file/directory
type EventType uint64

// PermissionType represents value indicating when the permission event must be requested.
type PermissionType int

const (
	// PermissionNone is used to indicate the listener is for notification events only.
	PermissionNone PermissionType = 0
	// PreContent is intended for event listeners that
	// need to access files before they contain their final data.
	PreContent PermissionType = 1
	// PostContent is intended for event listeners that
	// need to access files when they already contain their final content.
	PostContent PermissionType = 2
)

// FanotifyEvent represents a notification or a permission event from the kernel for the file,
// directory marked for watching.
// Notification events are merely informative and require
// no action to be taken by the receiving application with the exception being that the
// file descriptor provided within the event must be closed.
// Permission events are requests to the receiving application to decide whether permission
// for a file access shall be granted. For these events, the recipient must write a
// response which decides whether access is granted or not.
type FanotifyEvent struct {
	// Fd is the open file descriptor for the file/directory being watched
	Fd int
	// Path holds the name of the parent directory
	Path string
	// FileName holds the name of the file under the watched parent. The value is only available
	// on kernels 5.1 or greater (that support the receipt of events which contain additional information
	// about the underlying filesystem object correlated to an event).
	FileName string
	// EventTypes holds bit mask representing the operations
	EventTypes EventType
	// Pid Process ID of the process that caused the event
	Pid int
}

// NewFanotifyWatcher returns a fanotify listener from which filesystem
// notification events can be read. Each listener
// supports listening to events under a single mount point.
// For cases where multiple mount points need to be monitored
// multiple listener instances need to be used.
//
// Notification events are merely informative and require
// no action to be taken by the receiving application with the
// exception being that the file descriptor provided within the
// event must be closed.
//
// Permission events are requests to the receiving application to
// decide whether permission for a file access shall be granted.
// For these events, the recipient must write a response which decides
// whether access is granted or not.
//
// - mountPoint can be any file/directory under the mount point being
//   watched.
// - entireMount initializes the listener to monitor either the
//   the entire mount point (when true) or allows adding files
//   or directories to the listener's watch list (when false).
// - permType initializes the listener either notification events
//   or both notification and permission events.
//   Passing [PreContent] value allows the receipt of events
//   notifying that a file has been accessed and events for permission
//   decisions if a file may be accessed. It is intended for event listeners
//   that need to access files before they contain their final data. Passing
//   [PostContent] is intended for event listeners that need to access
//   files when they already contain their final content.
//
// The function returns a new instance of the listener. The fanotify flags
// are set based on the running kernel version. [ErrCapSysAdmin] is returned
// if the process does not have CAP_SYS_ADM capability.
//
//  - For Linux kernel version 5.0 and earlier no additional information about the underlying filesystem object is available.
//  - For Linux kernel versions 5.1 till 5.8 (inclusive) additional information about the underlying filesystem object is correlated to an event.
//  - For Linux kernel version 5.9 or later the modified file name is made available in the event.
func NewFanotifyWatcher(mountPoint string, entireMount bool, permType PermissionType) (*Watcher, error) {
	capSysAdmin, err := checkCapSysAdmin()
	if err != nil {
		return nil, err
	}
	if !capSysAdmin {
		return nil, ErrCapSysAdmin
	}
	isNotificationListener := true
	if permType == PreContent || permType == PostContent {
		isNotificationListener = false
	}
	w, err := newFanotifyWatcher(mountPoint, entireMount, isNotificationListener, permType)
	if err != nil {
		return nil, err
	}
	go w.start()
	return w, nil
}

// Stop stops the listener and closes the notification group and the events channel
func (w *Watcher) Stop() {
	if w == nil {
		return
	}
	// stop the listener
	unix.Write(int(w.stopper.w.Fd()), []byte("stop"))
	w.mountpoint.Close()
	w.stopper.r.Close()
	w.stopper.w.Close()
	close(w.Events)
}

// WatchMount adds or modifies the notification marks for the entire
// mount point.
// This method returns an [ErrWatchPath] if the listener was not initialized to monitor
// the entire mount point. To mark specific files or directories use [AddWatch] method.
// The following event types are considered invalid and WatchMount returns [ErrInvalidFlagCombination]
// for - [FileCreated], [FileAttribChanged], [FileMovedTo], [FileMovedFrom], [WatchedFileDeleted],
// [WatchedFileOrDirectoryDeleted], [FileDeleted], [FileOrDirectoryDeleted]
func (w *Watcher) WatchMount(eventTypes EventType) error {
	return w.fanotifyMark(w.mountpoint.Name(), unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT, uint64(eventTypes))
}

// UnwatchMount removes the notification marks for the entire mount point.
// This method returns an [ErrWatchPath] if the listener was not initialized to monitor
// the entire mount point. To unmark specific files or directories use [DeleteWatch] method.
func (w *Watcher) UnwatchMount(eventTypes EventType) error {
	return w.fanotifyMark(w.mountpoint.Name(), unix.FAN_MARK_REMOVE|unix.FAN_MARK_MOUNT, uint64(eventTypes))
}

// AddWatch adds or modifies the fanotify mark for the specified path.
// The events are only raised for the specified directory and does raise events
// for subdirectories. Calling AddWatch to mark the entire mountpoint results in
// [os.ErrInvalid]. To watch the entire mount point use [WatchMount] method.
// Certain flag combinations are known to cause issues.
//  - [FileCreated] cannot be or-ed / combined with [FileClosed]. The fanotify system does not generate any event for this combination.
//  - [FileOpened] with any of the event types containing OrDirectory causes an event flood for the directory and then stopping raising any events at all.
//  - [FileOrDirectoryOpened] with any of the other event types causes an event flood for the directory and then stopping raising any events at all.
func (w *Watcher) AddWatch(path string, eventTypes EventType) error {
	if w == nil {
		panic("nil listener")
	}
	if w.entireMount {
		return os.ErrInvalid
	}
	return w.fanotifyMark(path, unix.FAN_MARK_ADD, uint64(eventTypes|unix.FAN_EVENT_ON_CHILD))
}

// Allow sends an "allowed" response to the permission request event.
func (w *Watcher) Allow(e FanotifyEvent) {
	var response unix.FanotifyResponse
	response.Fd = int32(e.Fd)
	response.Response = unix.FAN_ALLOW
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &response)
	unix.Write(w.fd, buf.Bytes())
}

// Deny sends an "denied" response to the permission request event.
func (w *Watcher) Deny(e FanotifyEvent) {
	var response unix.FanotifyResponse
	response.Fd = int32(e.Fd)
	response.Response = unix.FAN_DENY
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &response)
	unix.Write(w.fd, buf.Bytes())
}

// DeleteWatch removes/unmarks the fanotify mark for the specified path.
// Calling DeleteWatch on the listener initialized to monitor the entire mount point
// results in [os.ErrInvalid]. Use [UnwatchMount] for deleting marks on the mount point.
func (w *Watcher) DeleteWatch(parentDir string, eventTypes EventType) error {
	if w.entireMount {
		return os.ErrInvalid
	}
	return w.fanotifyMark(parentDir, unix.FAN_MARK_REMOVE, uint64(eventTypes|unix.FAN_EVENT_ON_CHILD))
}

// ClearWatch stops watching for all event types
func (w *Watcher) ClearWatch() error {
	if w == nil {
		panic("nil listener")
	}
	if err := unix.FanotifyMark(w.fd, unix.FAN_MARK_FLUSH, 0, -1, ""); err != nil {
		return err
	}
	return nil
}

// Has returns true if event types (e) contains the passed in event type (et).
func (e EventType) Has(et EventType) bool {
	return e&et == et
}

// Or appends the specified event types to the set of event types to watch for
func (e EventType) Or(et EventType) EventType {
	return e | et
}

// String prints event types
func (e EventType) String() string {
	var eventTypes = map[EventType]string{
		unix.FAN_ACCESS:         "Access",
		unix.FAN_MODIFY:         "Modify",
		unix.FAN_CLOSE_WRITE:    "CloseWrite",
		unix.FAN_CLOSE_NOWRITE:  "CloseNoWrite",
		unix.FAN_OPEN:           "Open",
		unix.FAN_OPEN_EXEC:      "OpenExec",
		unix.FAN_ATTRIB:         "AttribChange",
		unix.FAN_CREATE:         "Create",
		unix.FAN_DELETE:         "Delete",
		unix.FAN_DELETE_SELF:    "SelfDelete",
		unix.FAN_MOVED_FROM:     "MovedFrom",
		unix.FAN_MOVED_TO:       "MovedTo",
		unix.FAN_MOVE_SELF:      "SelfMove",
		unix.FAN_OPEN_PERM:      "PermissionToOpen",
		unix.FAN_OPEN_EXEC_PERM: "PermissionToExecute",
		unix.FAN_ACCESS_PERM:    "PermissionToAccess",
	}
	var eventTypeList []string
	for k, v := range eventTypes {
		if e.Has(k) {
			eventTypeList = append(eventTypeList, v)
		}
	}
	return strings.Join(eventTypeList, ",")
}

func (e FanotifyEvent) String() string {
	return fmt.Sprintf("Fd:(%d), Pid:(%d), EventType:(%s), Path:(%s), Filename:(%s)", e.Fd, e.Pid, e.EventTypes, e.Path, e.FileName)
}
