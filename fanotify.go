package fsnotify

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

type FanotifyWatcher struct {
	fd       int
	done     chan struct{} // Channel for sending a "quit message" to the reader goroutine
	doneResp chan struct{} // Channel to respond to Close
	Events   chan Event
	Errors   chan error
	poller   *fdPoller
}

func NewFanotifyWatcher() (*FanotifyWatcher, error) {
	fd, err := unix.FanotifyInit(unix.FAN_CLASS_NOTIF, unix.O_RDONLY|unix.O_LARGEFILE)
	if fd < 0 {
		return nil, err
	}

	poller, err := newFdPoller(fd)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	fw := &FanotifyWatcher{
		fd:       fd,
		done:     make(chan struct{}),
		doneResp: make(chan struct{}),
		Events:   make(chan Event),
		Errors:   make(chan error),
		poller:   poller,
	}
	go fw.readEvents()
	return fw, nil
}

func (fw *FanotifyWatcher) Add(path string) error {
	err := unix.FanotifyMark(
		fw.fd,
		unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT,
		unix.FAN_CLOSE_WRITE,
		unix.AT_FDCWD,
		path,
	)
	if err != nil {
		log.Printf("unix.FanotifyMark(%d, ..., %s) failed: %s\n", fw.fd, path, err.Error())
	}
	return err
}

func (fw *FanotifyWatcher) readEvents() {
	var (
		buf   [unix.FAN_EVENT_METADATA_LEN * 4096]byte // Buffer for a maximum of 4096 raw events
		n     int                                      // Number of bytes read with read()
		errno error                                    // Syscall errno
		ok    bool                                     // For poller.wait
	)

	defer close(fw.doneResp)
	defer close(fw.Errors)
	defer close(fw.Events)
	defer unix.Close(fw.fd)
	defer fw.poller.close()

	for {
		// See if we have been closed.
		if fw.isClosed() {
			return
		}

		ok, errno = fw.poller.wait()
		if errno != nil {
			select {
			case fw.Errors <- errno:
			case <-fw.done:
				return
			}
			continue
		}

		if !ok {
			continue
		}

		n, errno = unix.Read(fw.fd, buf[:])
		// If a signal interrupted execution, see if we've been asked to close, and try again.
		// http://man7.org/linux/man-pages/man7/signal.7.html :
		if errno == unix.EINTR {
			continue
		}

		// unix.Read might have been woken up by Close. If so, we're done.
		if fw.isClosed() {
			return
		}

		if n < unix.FAN_EVENT_METADATA_LEN {
			var err error
			if n == 0 {
				// If EOF is received. This should really never happen.
				err = io.EOF
			} else if n < 0 {
				// If an error occurred while reading.
				err = errno
			} else {
				// Read was too short.
				err = errors.New("notify: short read in readEvents()")
			}
			select {
			case fw.Errors <- err:
			case <-fw.done:
				return
			}
			continue
		}

		var offset uint32
		for offset <= uint32(n-unix.FAN_EVENT_METADATA_LEN) {
			raw := (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[offset]))
			if !(raw.Event_len >= unix.FAN_EVENT_METADATA_LEN && raw.Event_len <= uint32(n)-offset) {
				continue
			}

			mask := raw.Mask
			if mask&unix.FAN_Q_OVERFLOW != 0 {
				select {
				case fw.Errors <- ErrEventOverflow:
				case <-fw.done:
					return
				}
			}

			path, errno := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", raw.Fd))
			if errno != nil {
				select {
				case fw.Errors <- errno:
				case <-fw.done:
					return
				}
			}

			fw.Events <- newFanotifyEvent(path, uintptr(raw.Fd))
			offset += raw.Event_len
		}
	}
}

func (fw *FanotifyWatcher) isClosed() bool {
	select {
	case <-fw.done:
		return true
	default:
		return false
	}
}

func newFanotifyEvent(name string, fd uintptr) Event {
	return Event{Name: name, Op: Write, File: os.NewFile(fd, name)}
}

func (fw *FanotifyWatcher) Close() error {
	if fw.isClosed() {
		return nil
	}

	// Send 'close' signal to goroutine, and set the Watcher to closed.
	close(fw.done)

	// Wake up goroutine
	_ = fw.poller.wake()

	// Wait for goroutine to close
	<-fw.doneResp

	return nil
}
