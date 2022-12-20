//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import (
	"bytes"
	"encoding/binary"
	"errors"

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
	if !w.fanotify {
		panic("expected fanotify watcher")
	}
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
	if !w.fanotify {
		panic("expected fanotify watcher")
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

	return w.fanotifyMark(w.mountpoint.Name(), unix.FAN_MARK_REMOVE|unix.FAN_MARK_MOUNT, uint64(eventTypes))
}

// AddPermissions adds the ability to make access permission decisions
// for file or directory. The function returns an error [ErrInvalidFlagValue]
// if there are no requests sent.
//
// This operation is only available for Fanotify watcher type i.e.
// ([NewFanotifyWatcher]). The method panics if the watcher is an
// instance from [NewWatcher].
func (w *Watcher) AddPermissions(name string, requests ...PermissionRequest) error {
	if !w.fanotify {
		panic("expected fanotify watcher")
	}
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
	if !w.fanotify {
		panic("expected fanotify watcher")
	}
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
	if !w.fanotify {
		panic("expected fanotify watcher")
	}
	var response unix.FanotifyResponse
	response.Fd = int32(e.Fd)
	response.Response = unix.FAN_DENY
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &response)
	unix.Write(w.fd, buf.Bytes())
}
