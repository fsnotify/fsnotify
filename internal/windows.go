//go:build windows
// +build windows

package internal

import (
	"errors"
)

// Just a dummy.
var (
	SyscallEACCES = errors.New("dummy")
	UnixEACCES    = errors.New("dummy")
)

func SetRlimit()                                    {}
func Maxfiles() uint64                              { return 1<<64 - 1 }
func Mkfifo(path string, mode uint32) error         { return errors.New("no FIFOs on Windows") }
func Mknod(path string, mode uint32, dev int) error { return errors.New("no device nodes on Windows") }
