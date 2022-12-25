//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var (
	// ErrCapSysAdmin indicates caller is missing CAP_SYS_ADMIN permissions
	ErrCapSysAdmin = errors.New("require CAP_SYS_ADMIN capability")
	// ErrInvalidFlagValue indicates flag value is invalid
	ErrInvalidFlagValue = errors.New("invalid flag value")
)

// NotificationClass represents value indicating when the permission event must be requested.
type NotificationClass int

// PermissionRequest represents the request for which the permission event is created.
type PermissionRequest uint64

const (
	// PermissionNone is used to indicate the listener is for notification events only.
	PermissionNone NotificationClass = 0
	// PreContent is intended for event listeners that
	// need to access files before they contain their final data.
	PreContent NotificationClass = 1
	// PostContent is intended for event listeners that
	// need to access files when they already contain their final content.
	PostContent NotificationClass = 2

	// PermissionRequestToOpen create's an event when a permission to open a file or
	// directory is requested.
	PermissionRequestToOpen PermissionRequest = PermissionRequest(fileOpenPermission)
	// PermissionRequestToAccess create's an event when a permission to read a file or
	// directory is requested.
	PermissionRequestToAccess PermissionRequest = PermissionRequest(fileAccessPermission)
	// PermissionRequestToExecute create an event when a permission to open a file for
	// execution is requested.
	PermissionRequestToExecute PermissionRequest = PermissionRequest(fileOpenToExecutePermission)
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
	Events chan Event

	fd                 int
	flags              uint     // flags passed to fanotify_init
	mountpoint         *os.File // mount fd is the file descriptor of the mountpoint
	kernelMajorVersion int
	kernelMinorVersion int
	entireMount        bool
	notificationOnly   bool
	stopper            struct {
		r *os.File
		w *os.File
	}
	// FanotifyEvents holds either notification events for the watched file/directory.
	FanotifyEvents chan FanotifyEvent
	// PermissionEvents holds permission request events for the watched file/directory.
	//   fsnotify.PermissionToOpen    Permission request to open a file or directory. (Applicable only to fanotify watcher.)
	//   fsnotify.PermissionToExecute Permission to open file for execution. (Applicable only to fanotify watcher.)
	//   fsnotify.PermissionToRead    Permission to read a file or directory. (Applicable only to fanotify watcher.)
	PermissionEvents chan FanotifyEvent
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
//  - For Linux kernel version 5.0 and earlier no additional information about
//    the underlying filesystem object is available.
//  - For Linux kernel versions 5.1 till 5.8 (inclusive) additional information
//    about the underlying filesystem object is correlated to an event.
//  - For Linux kernel version 5.9 or later the modified file name is made available
//    in the event.
func NewFanotifyWatcher(mountPoint string, entireMount bool, permType NotificationClass) (*Watcher, error) {
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

// AddMount adds watch to monitor the entire mountpoint for
// file or directory accessed, file opened, file modified,
// file closed with no write, file closed with write,
// file opened for execution events. The method returns
// [ErrInvalidFlagValue] if the watcher was not initialized
// with [NewFanotifyWatcher] entireMount boolean flag set to
// true.
//
// This operation is only available for Fanotify watcher type i.e.
// ([NewFanotifyWatcher]). The method panics if the watcher is an
// instance from [NewWatcher].
func (w *Watcher) AddMount() error {
	if !w.entireMount {
		return ErrInvalidFlagValue
	}
	var eventTypes fanotifyEventType
	eventTypes = fileAccessed |
		fileOrDirectoryAccessed |
		fileModified |
		fileClosedAfterWrite |
		fileClosedWithNoWrite |
		fileOpened |
		fileOrDirectoryOpened |
		fileOpenedForExec

	return w.fanotifyMark(w.mountpoint.Name(), unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT, uint64(eventTypes))
}

// RemoveMount removes watch from the mount point.
//
// This operation is only available for Fanotify watcher type i.e.
// ([NewFanotifyWatcher]). The method panics if the watcher is an
// instance from [NewWatcher].
func (w *Watcher) RemoveMount() error {
	var eventTypes fanotifyEventType
	eventTypes = fileAccessed |
		fileOrDirectoryAccessed |
		fileModified |
		fileClosedAfterWrite |
		fileClosedWithNoWrite |
		fileOpened |
		fileOrDirectoryOpened |
		fileOpenedForExec

	return w.fanotifyMark(w.mountpoint.Name(), unix.FAN_MARK_REMOVE|unix.FAN_MARK_MOUNT, uint64(eventTypes))
}

// Close stops the watcher and closes the event channels
func (w *Watcher) Close() {
	unix.Write(int(w.stopper.w.Fd()), []byte("stop"))
	w.mountpoint.Close()
	w.stopper.r.Close()
	w.stopper.w.Close()
	close(w.Events)
}

// AddPermissions adds the ability to make access permission decisions
// for file or directory. The function returns an error [ErrInvalidFlagValue]
// if there are no requests sent.
//
// This operation is only available for Fanotify watcher type i.e.
// ([NewFanotifyWatcher]). The method panics if the watcher is an
// instance from [NewWatcher].
func (w *Watcher) AddPermissions(name string, requests ...PermissionRequest) error {
	if len(requests) == 0 {
		return ErrInvalidFlagValue
	}
	var eventTypes fanotifyEventType
	for _, r := range requests {
		eventTypes |= fanotifyEventType(r)
	}
	return w.fanotifyMark(name, unix.FAN_MARK_ADD, uint64(eventTypes|unix.FAN_EVENT_ON_CHILD))
}

// Allow sends an "allowed" response to the permission request event.
//
// This operation is only available for Fanotify watcher type i.e.
// ([NewFanotifyWatcher]). The method panics if the watcher is an
// instance from [NewWatcher].
func (w *Watcher) Allow(e FanotifyEvent) {
	var response unix.FanotifyResponse
	response.Fd = int32(e.Fd)
	response.Response = unix.FAN_ALLOW
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &response)
	unix.Write(w.fd, buf.Bytes())
}

// Deny sends an "denied" response to the permission request event.
//
// This operation is only available for Fanotify watcher type i.e.
// ([NewFanotifyWatcher]). The method panics if the watcher is an
// instance from [NewWatcher].
func (w *Watcher) Deny(e FanotifyEvent) {
	var response unix.FanotifyResponse
	response.Fd = int32(e.Fd)
	response.Response = unix.FAN_DENY
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &response)
	unix.Write(w.fd, buf.Bytes())
}
