//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import "golang.org/x/sys/unix"

const (
	// FileAccessed event when a file is accessed
	FileAccessed EventType = unix.FAN_ACCESS

	// FileOrDirectoryAccessed event when a file or directory is accessed
	FileOrDirectoryAccessed EventType = unix.FAN_ACCESS | unix.FAN_ONDIR

	// FileModified event when a file is modified
	FileModified EventType = unix.FAN_MODIFY

	// FileClosedAfterWrite event when a file is closed
	FileClosedAfterWrite EventType = unix.FAN_CLOSE_WRITE

	// FileClosedWithNoWrite event when a file is closed without writing
	FileClosedWithNoWrite EventType = unix.FAN_CLOSE_NOWRITE

	// FileClosed event when a file is closed after write or no write
	FileClosed EventType = unix.FAN_CLOSE_WRITE | unix.FAN_CLOSE_NOWRITE

	// FileOpened event when a file is opened
	FileOpened EventType = unix.FAN_OPEN

	// FileOrDirectoryOpened event when a file or directory is opened
	FileOrDirectoryOpened EventType = unix.FAN_OPEN | unix.FAN_ONDIR

	// FileOpenedForExec event when a file is opened with the intent to be executed.
	// Requires Linux kernel 5.0 or later
	FileOpenedForExec EventType = unix.FAN_OPEN_EXEC

	// FileAttribChanged event when a file attribute has changed
	// Requires Linux kernel 5.1 or later (requires FID)
	FileAttribChanged EventType = unix.FAN_ATTRIB

	// FileOrDirectoryAttribChanged event when a file or directory attribute has changed
	// Requires Linux kernel 5.1 or later (requires FID)
	FileOrDirectoryAttribChanged EventType = unix.FAN_ATTRIB | unix.FAN_ONDIR

	// FileCreated event when file a has been created
	// Requires Linux kernel 5.1 or later (requires FID)
	// BUG FileCreated does not work with FileClosed, FileClosedAfterWrite or FileClosedWithNoWrite
	FileCreated EventType = unix.FAN_CREATE

	// FileOrDirectoryCreated event when a file or directory has been created
	// Requires Linux kernel 5.1 or later (requires FID)
	FileOrDirectoryCreated EventType = unix.FAN_CREATE | unix.FAN_ONDIR

	// FileDeleted event when file a has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	FileDeleted EventType = unix.FAN_DELETE

	// FileOrDirectoryDeleted event when a file or directory has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	FileOrDirectoryDeleted EventType = unix.FAN_DELETE | unix.FAN_ONDIR

	// WatchedFileDeleted event when a watched file has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	WatchedFileDeleted EventType = unix.FAN_DELETE_SELF

	// WatchedFileOrDirectoryDeleted event when a watched file or directory has been deleted
	// Requires Linux kernel 5.1 or later (requires FID)
	WatchedFileOrDirectoryDeleted EventType = unix.FAN_DELETE_SELF | unix.FAN_ONDIR

	// FileMovedFrom event when a file has been moved from the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	FileMovedFrom EventType = unix.FAN_MOVED_FROM

	// FileOrDirectoryMovedFrom event when a file or directory has been moved from the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	FileOrDirectoryMovedFrom EventType = unix.FAN_MOVED_FROM | unix.FAN_ONDIR

	// FileMovedTo event when a file has been moved to the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	FileMovedTo EventType = unix.FAN_MOVED_TO

	// FileOrDirectoryMovedTo event when a file or directory has been moved to the watched directory
	// Requires Linux kernel 5.1 or later (requires FID)
	FileOrDirectoryMovedTo EventType = unix.FAN_MOVED_TO | unix.FAN_ONDIR

	// WatchedFileMoved event when a watched file has moved
	// Requires Linux kernel 5.1 or later (requires FID)
	WatchedFileMoved EventType = unix.FAN_MOVE_SELF

	// WatchedFileOrDirectoryMoved event when a watched file or directory has moved
	// Requires Linux kernel 5.1 or later (requires FID)
	WatchedFileOrDirectoryMoved EventType = unix.FAN_MOVE_SELF | unix.FAN_ONDIR

	// FileOpenPermission event when a permission to open a file or directory is requested
	FileOpenPermission EventType = unix.FAN_OPEN_PERM

	// FileOpenToExecutePermission event when a permission to open a file for
	// execution is requested
	FileOpenToExecutePermission EventType = unix.FAN_OPEN_EXEC_PERM

	// FileAccessPermission event when a permission to read a file or directory is requested
	FileAccessPermission EventType = unix.FAN_ACCESS_PERM
)
