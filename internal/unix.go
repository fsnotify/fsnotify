//go:build !windows
// +build !windows

package internal

import (
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	SyscallEACCES = syscall.EACCES
	UnixEACCES    = unix.EACCES
)
