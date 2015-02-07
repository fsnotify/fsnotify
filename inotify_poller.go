// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux

package fsnotify

import (
	"errors"
	"os"
	"syscall"
)

type fdPoller struct {
	fd   int    // File descriptor (as returned by the inotify_init() syscall)
	epfd int    // Epoll file descriptor
	pipe [2]int // Pipe for waking up
}

// Create a new inotify poller.
// This creates an inotify handler, and an epoll handler.
func newFdPoller() (*fdPoller, error) {
	var errno error
	poller := new(fdPoller)

	// Create inotify fd
	poller.fd, errno = syscall.InotifyInit()
	if poller.fd == -1 {
		return nil, os.NewSyscallError("inotify_init", errno)
	}
	// Create epoll fd
	poller.epfd, errno = syscall.EpollCreate(1)
	if poller.epfd == -1 {
		syscall.Close(poller.fd)
		return nil, os.NewSyscallError("epoll_create", errno)
	}
	// Create pipe; pipe[0] is the read end, pipe[1] the write end.
	errno = syscall.Pipe(poller.pipe[:])
	if errno != nil {
		syscall.Close(poller.fd)
		syscall.Close(poller.epfd)
		return nil, os.NewSyscallError("pipe", errno)
	}

	// Register inotify fd with epoll
	event := syscall.EpollEvent{
		Fd:     int32(poller.fd),
		Events: syscall.EPOLLIN,
	}
	errno = syscall.EpollCtl(poller.epfd, syscall.EPOLL_CTL_ADD, poller.fd, &event)
	if errno != nil {
		syscall.Close(poller.fd)
		syscall.Close(poller.epfd)
		syscall.Close(poller.pipe[0])
		syscall.Close(poller.pipe[1])
		return nil, os.NewSyscallError("epoll_ctl", errno)
	}

	// Register pipe fd with epoll
	event = syscall.EpollEvent{
		Fd:     int32(poller.pipe[0]),
		Events: syscall.EPOLLIN,
	}
	errno = syscall.EpollCtl(poller.epfd, syscall.EPOLL_CTL_ADD, poller.pipe[0], &event)
	if errno != nil {
		syscall.Close(poller.fd)
		syscall.Close(poller.epfd)
		syscall.Close(poller.pipe[0])
		syscall.Close(poller.pipe[1])
		return nil, os.NewSyscallError("epoll_ctl", errno)
	}

	return poller, nil
}

// Wait using epoll, then read from inotify.
// Returns true if something is ready to be read,
// false if there is not.
func (poller *fdPoller) wait() (bool, error) {
	events := make([]syscall.EpollEvent, 7)
	for {
		n, errno := syscall.EpollWait(poller.epfd, events, -1)
		if n == -1 {
			if errno == syscall.EINTR {
				continue
			}
			return false, os.NewSyscallError("epoll_wait", errno)
		}
		if n == 0 {
			// If there are no events, try again.
			continue
		}
		if n > 6 {
			// This should never happen.
			return false, errors.New("epoll_wait returned more events than I know what to do with")
		}
		ready := events[:n]
		epollhup := false
		epollerr := false
		epollin := false
		epollpipehup := false
		epollpipein := false
		for _, event := range ready {
			if event.Fd == int32(poller.fd) {
				if event.Events&syscall.EPOLLHUP != 0 {
					// This should not happen, but if it does, treat it as a wakeup.
					epollhup = true
				}
				if event.Events&syscall.EPOLLERR != 0 {
					// If an error is waiting on the file descriptor, we should pretend
					// something is ready to read, and let syscall.Read pick up the error.
					epollerr = true
				}
				if event.Events&syscall.EPOLLIN != 0 {
					// There is data to read.
					epollin = true
				}
			}
			if event.Fd == int32(poller.pipe[0]) {
				if event.Events&syscall.EPOLLHUP != 0 {
					// Write pipe descriptor was closed, by us. This means we're closing down the
					// watcher, and we should wake up.
					epollpipehup = true
				}
				if event.Events&syscall.EPOLLERR != 0 {
					// If an error is waiting on the pipe file descriptor.
					// This is an absolute mystery, and should never ever happen.
					return false, errors.New("Error on the pipe descriptor.")
				}
				if event.Events&syscall.EPOLLIN != 0 {
					// This is a regular wakeup.
					epollpipein = true
					// Clear the buffer.
					err := poller.clearWake()
					if err != nil {
						return false, err
					}
				}
			}
		}

		if epollerr {
			return true, nil
		}
		if epollhup || epollpipehup || epollpipein {
			return false, nil
		}
		if epollin {
			return true, nil
		}
		return false, errors.New("Epoll failed to generate any of the only six possibilities.")
	}
}

// Close the write end of the poller.
func (poller *fdPoller) wake() error {
	buf := make([]byte, 1)
	n, errno := syscall.Write(poller.pipe[1], buf)
	if n == -1 {
		return os.NewSyscallError("write", errno)
	}
	return nil
}

func (poller *fdPoller) clearWake() error {
	buf := make([]byte, 100)
	n, errno := syscall.Read(poller.pipe[0], buf)
	if n == -1 {
		return os.NewSyscallError("read", errno)
	}
	return nil
}

// Close all file descriptors.
func (poller *fdPoller) close() {
	syscall.Close(poller.pipe[1])
	syscall.Close(poller.pipe[0])
	syscall.Close(poller.fd)
	syscall.Close(poller.epfd)
}
