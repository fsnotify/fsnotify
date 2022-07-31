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
