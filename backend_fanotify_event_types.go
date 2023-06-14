//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import "golang.org/x/sys/unix"

const (
	// fileAccessed event when a file is accessed
	fileAccessed fanotifyEventType = unix.FAN_ACCESS

	// fileOrDirectoryAccessed event when a file or directory is accessed
	fileOrDirectoryAccessed fanotifyEventType = unix.FAN_ACCESS | unix.FAN_ONDIR

	// fileModified event when a file is modified
	fileModified fanotifyEventType = unix.FAN_MODIFY

	// fileClosedAfterWrite event when a file is closed
	fileClosedAfterWrite fanotifyEventType = unix.FAN_CLOSE_WRITE

	// fileClosedWithNoWrite event when a file is closed without writing
	fileClosedWithNoWrite fanotifyEventType = unix.FAN_CLOSE_NOWRITE

	// fileClosed event when a file is closed after write or no write
	fileClosed fanotifyEventType = unix.FAN_CLOSE_WRITE | unix.FAN_CLOSE_NOWRITE

	// fileOpened event when a file is opened
	fileOpened fanotifyEventType = unix.FAN_OPEN

	// fileOrDirectoryOpened event when a file or directory is opened
	fileOrDirectoryOpened fanotifyEventType = unix.FAN_OPEN | unix.FAN_ONDIR

	// fileOpenedForExec event when a file is opened with the intent to be executed.
	// Requires Linux kernel 5.0 or later
	fileOpenedForExec fanotifyEventType = unix.FAN_OPEN_EXEC

	// fileAttribChanged event when a file attribute has changed
	// Requires Linux kernel 5.1 or later (requires FID)
	fileAttribChanged fanotifyEventType = unix.FAN_ATTRIB

	// fileOrDirectoryAttribChanged event when a file or directory attribute has changed
	// Requires Linux kernel 5.1 or later (requires FID)
	fileOrDirectoryAttribChanged fanotifyEventType = unix.FAN_ATTRIB | unix.FAN_ONDIR

	// fileCreated event when file a has been created
	// Requires Linux kernel 5.1 or later (requires FID)
	// BUG FileCreated does not work with FileClosed, FileClosedAfterWrite or FileClosedWithNoWrite
	fileCreated fanotifyEventType = unix.FAN_CREATE

	// fileOrDirectoryCreated event when a file or directory has been created
	// Requires Linux kernel 5.1 or later (requires FID)
	fileOrDirectoryCreated fanotifyEventType = unix.FAN_CREATE | unix.FAN_ONDIR

	// fileDeleted event when file a has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	fileDeleted fanotifyEventType = unix.FAN_DELETE

	// fileOrDirectoryDeleted event when a file or directory has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	fileOrDirectoryDeleted fanotifyEventType = unix.FAN_DELETE | unix.FAN_ONDIR

	// watchedFileDeleted event when a watched file has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	watchedFileDeleted fanotifyEventType = unix.FAN_DELETE_SELF

	// watchedFileOrDirectoryDeleted event when a watched file or directory has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	watchedFileOrDirectoryDeleted fanotifyEventType = unix.FAN_DELETE_SELF | unix.FAN_ONDIR

	// fileMovedFrom event when a file has been moved from the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	fileMovedFrom fanotifyEventType = unix.FAN_MOVED_FROM

	// fileOrDirectoryMovedFrom event when a file or directory has been moved from the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	fileOrDirectoryMovedFrom fanotifyEventType = unix.FAN_MOVED_FROM | unix.FAN_ONDIR

	// fileMovedTo event when a file has been moved to the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	fileMovedTo fanotifyEventType = unix.FAN_MOVED_TO

	// fileOrDirectoryMovedTo event when a file or directory has been moved to the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	fileOrDirectoryMovedTo fanotifyEventType = unix.FAN_MOVED_TO | unix.FAN_ONDIR

	// watchedFileMoved event when a watched file has moved
	// Requires Linux kernel 5.1 or later (requires FID)
	watchedFileMoved fanotifyEventType = unix.FAN_MOVE_SELF

	// watchedFileOrDirectoryMoved event when a watched file or directory has moved
	// Requires Linux kernel 5.1 or later (requires FID)
	watchedFileOrDirectoryMoved fanotifyEventType = unix.FAN_MOVE_SELF | unix.FAN_ONDIR

	// fileOpenPermission event when a permission to open a file or directory is requested
	fileOpenPermission fanotifyEventType = unix.FAN_OPEN_PERM

	// fileOpenToExecutePermission event when a permission to open a file for
	// execution is requested
	fileOpenToExecutePermission fanotifyEventType = unix.FAN_OPEN_EXEC_PERM

	// fileAccessPermission event when a permission to read a file or directory is requested
	fileAccessPermission fanotifyEventType = unix.FAN_ACCESS_PERM
)
