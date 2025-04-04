//go:build darwin

package fsnotify

import "golang.org/x/sys/unix"

const (
	openMode     = unix.O_EVTONLY | unix.O_CLOEXEC
	openNofollow = unix.O_SYMLINK
)
