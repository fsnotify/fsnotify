//go:build !windows && !darwin && !freebsd && !plan9

package internal

import (
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	ErrSyscallEACCES = syscall.EACCES
	ErrUnixEACCES    = unix.EACCES
)

func Mkfifo(path string, mode uint32) error         { return unix.Mkfifo(path, mode) }
func Mknod(path string, mode uint32, dev int) error { return unix.Mknod(path, mode, dev) }
