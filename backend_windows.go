//go:build windows

// Windows backend based on ReadDirectoryChangesW()
//
// https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-readdirectorychangesw

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify/internal"
	"golang.org/x/sys/windows"
)

type readDirChangesW struct {
	Events chan Event
	Errors chan error

	port  windows.Handle // Handle to completion port
	input chan *input    // Inputs to the reader are sent on this channel
	done  chan chan<- error

	mu      sync.Mutex // Protects access to watches, closed
	watches watchMap   // Map of watches (key: i-number)
	closed  bool       // Set to true when Close() is first called

	// Every ReadDirectoryChangesW call that was issued and whose completion
	// packet has not been dequeued yet. The kernel holds the raw address of
	// each op's OVERLAPPED and buffer until that packet is posted, and a raw
	// address is not a GC root, so this set is what keeps them alive. Only
	// the I/O thread touches it.
	ops map[*readOp]struct{}
}

// readOp is one outstanding ReadDirectoryChangesW call. ov must stay the first
// field: the completion packet hands back &op.ov, and the reader recovers the
// op from it.
type readOp struct {
	ov    windows.Overlapped
	watch *watch
	buf   []byte
}

// How long Close waits for the completion packets of cancelled reads before
// it gives up and pins them for the life of the process.
const closeDrainTimeout = 5 * time.Second

// Ops whose completions never arrived before Close gave up. Kept reachable
// forever because the kernel may still write into them.
var (
	abandonedOpsMu sync.Mutex
	abandonedOps   []*readOp
)

var defaultBufferSize = 50

func newBackend(ev chan Event, errs chan error) (backend, error) {
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 0)
	if err != nil {
		return nil, os.NewSyscallError("CreateIoCompletionPort", err)
	}
	w := &readDirChangesW{
		Events:  ev,
		Errors:  errs,
		port:    port,
		watches: make(watchMap),
		ops:     make(map[*readOp]struct{}),
		input:   make(chan *input, 1),
		done:    make(chan chan<- error, 1),
	}
	go w.readEvents()
	return w, nil
}

func (w *readDirChangesW) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *readDirChangesW) sendEvent(name, renamedFrom string, mask uint64) bool {
	if mask == 0 {
		return false
	}

	event := w.newEvent(name, uint32(mask))
	event.renamedFrom = renamedFrom
	select {
	case ch := <-w.done:
		w.done <- ch
	case w.Events <- event:
	}
	return true
}

// Returns true if the error was sent, or false if watcher is closed.
func (w *readDirChangesW) sendError(err error) bool {
	if err == nil {
		return true
	}
	select {
	case <-w.done:
		return false
	case w.Errors <- err:
		return true
	}
}

func (w *readDirChangesW) Close() error {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()

	// Send "done" message to the reader goroutine
	ch := make(chan error)
	w.done <- ch
	if err := w.wakeupReader(); err != nil {
		return err
	}
	return <-ch
}

func (w *readDirChangesW) Add(name string) error { return w.AddWith(name) }

func (w *readDirChangesW) AddWith(name string, opts ...addOpt) error {
	if w.isClosed() {
		return ErrClosed
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), filepath.ToSlash(name))
	}

	with := getOptions(opts...)
	if !w.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}
	if with.bufsize < 4096 {
		return fmt.Errorf("fsnotify.WithBufferSize: buffer size cannot be smaller than 4096 bytes")
	}

	in := &input{
		op:      opAddWatch,
		path:    filepath.Clean(name),
		flags:   sysFSALLEVENTS,
		reply:   make(chan error),
		bufsize: with.bufsize,
	}
	w.input <- in
	if err := w.wakeupReader(); err != nil {
		return err
	}
	return <-in.reply
}

func (w *readDirChangesW) Remove(name string) error {
	if w.isClosed() {
		return nil
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), filepath.ToSlash(name))
	}

	in := &input{
		op:    opRemoveWatch,
		path:  filepath.Clean(name),
		reply: make(chan error),
	}
	w.input <- in
	if err := w.wakeupReader(); err != nil {
		return err
	}
	return <-in.reply
}

func (w *readDirChangesW) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	entries := make([]string, 0, len(w.watches))
	for _, entry := range w.watches {
		for _, watchEntry := range entry {
			for name := range watchEntry.names {
				entries = append(entries, filepath.Join(watchEntry.path, name))
			}
			// the directory itself is being watched
			if watchEntry.mask != 0 {
				entries = append(entries, watchEntry.path)
			}
		}
	}

	return entries
}

// These options are from the old golang.org/x/exp/winfsnotify, where you could
// add various options to the watch. This has long since been removed.
//
// The "sys" in the name is misleading as they're not part of any "system".
//
// This should all be removed at some point, and just use windows.FILE_NOTIFY_*
const (
	sysFSALLEVENTS  = 0xfff
	sysFSCREATE     = 0x100
	sysFSDELETE     = 0x200
	sysFSDELETESELF = 0x400
	sysFSMODIFY     = 0x2
	sysFSMOVE       = 0xc0
	sysFSMOVEDFROM  = 0x40
	sysFSMOVEDTO    = 0x80
	sysFSMOVESELF   = 0x800
	sysFSIGNORED    = 0x8000
)

func (w *readDirChangesW) newEvent(name string, mask uint32) Event {
	e := Event{Name: name}
	if mask&sysFSCREATE == sysFSCREATE || mask&sysFSMOVEDTO == sysFSMOVEDTO {
		e.Op |= Create
	}
	if mask&sysFSDELETE == sysFSDELETE || mask&sysFSDELETESELF == sysFSDELETESELF {
		e.Op |= Remove
	}
	if mask&sysFSMODIFY == sysFSMODIFY {
		e.Op |= Write
	}
	if mask&sysFSMOVE == sysFSMOVE || mask&sysFSMOVESELF == sysFSMOVESELF || mask&sysFSMOVEDFROM == sysFSMOVEDFROM {
		e.Op |= Rename
	}
	return e
}

const (
	opAddWatch = iota
	opRemoveWatch
)

const (
	provisional uint64 = 1 << (32 + iota)
)

type input struct {
	op      int
	path    string
	flags   uint32
	bufsize int
	reply   chan error
}

type inode struct {
	handle windows.Handle
	volume uint32
	index  uint64
}

type watch struct {
	ino     *inode            // i-number
	recurse bool              // Recursive watch?
	path    string            // Directory path
	mask    uint64            // Directory itself is being watched with these notify flags
	names   map[string]uint64 // Map of names being watched and their notify flags
	rename  string            // Remembers the old name while renaming a file

	// The fields below are owned by the I/O thread.
	bufsize int     // Size of each read buffer
	spare   []byte  // A read buffer no outstanding op owns, reused by the next read
	cur     *readOp // The read whose results are wanted; nil when none is outstanding
	retired bool    // Handle closed and removed from watches; never read again
}

type (
	indexMap map[uint64]*watch
	watchMap map[uint32]indexMap
)

func (w *readDirChangesW) wakeupReader() error {
	err := windows.PostQueuedCompletionStatus(w.port, 0, 0, nil)
	if err != nil {
		return os.NewSyscallError("PostQueuedCompletionStatus", err)
	}
	return nil
}

func (w *readDirChangesW) getDir(pathname string) (dir string, err error) {
	attr, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(pathname))
	if err != nil {
		return "", os.NewSyscallError("GetFileAttributes", err)
	}
	if attr&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		dir = pathname
	} else {
		dir, _ = filepath.Split(pathname)
		dir = filepath.Clean(dir)
	}
	return
}

func (w *readDirChangesW) getIno(path string) (ino *inode, err error) {
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path),
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, os.NewSyscallError("CreateFile", err)
	}

	var fi windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &fi)
	if err != nil {
		windows.CloseHandle(h)
		return nil, os.NewSyscallError("GetFileInformationByHandle", err)
	}
	ino = &inode{
		handle: h,
		volume: fi.VolumeSerialNumber,
		index:  uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow),
	}
	return ino, nil
}

// Must run within the I/O thread.
func (m watchMap) get(ino *inode) *watch {
	if i := m[ino.volume]; i != nil {
		return i[ino.index]
	}
	return nil
}

// Must run within the I/O thread.
func (m watchMap) set(ino *inode, watch *watch) {
	i := m[ino.volume]
	if i == nil {
		i = make(indexMap)
		m[ino.volume] = i
	}
	i[ino.index] = watch
}

// Must run within the I/O thread.
func (w *readDirChangesW) addWatch(pathname string, flags uint64, bufsize int) error {
	pathname, recurse := recursivePath(pathname)

	dir, err := w.getDir(pathname)
	if err != nil {
		return err
	}

	ino, err := w.getIno(dir)
	if err != nil {
		return err
	}
	w.mu.Lock()
	watchEntry := w.watches.get(ino)
	w.mu.Unlock()
	if watchEntry == nil {
		_, err := windows.CreateIoCompletionPort(ino.handle, w.port, 0, 0)
		if err != nil {
			windows.CloseHandle(ino.handle)
			return os.NewSyscallError("CreateIoCompletionPort", err)
		}
		watchEntry = &watch{
			ino:     ino,
			path:    dir,
			names:   make(map[string]uint64),
			recurse: recurse,
			bufsize: bufsize,
			spare:   make([]byte, bufsize),
		}
		w.mu.Lock()
		w.watches.set(ino, watchEntry)
		w.mu.Unlock()
		flags |= provisional
	} else {
		windows.CloseHandle(ino.handle)
	}
	w.mu.Lock()
	if pathname == dir {
		watchEntry.mask |= flags
	} else {
		watchEntry.names[filepath.Base(pathname)] |= flags
	}
	w.mu.Unlock()

	err = w.startRead(watchEntry)
	if err != nil {
		return err
	}

	w.mu.Lock()
	if pathname == dir {
		watchEntry.mask &= ^provisional
	} else {
		watchEntry.names[filepath.Base(pathname)] &= ^provisional
	}
	w.mu.Unlock()
	return nil
}

// Must run within the I/O thread.
func (w *readDirChangesW) remWatch(pathname string) error {
	pathname, recurse := recursivePath(pathname)

	dir, err := w.getDir(pathname)
	if err != nil {
		return err
	}
	ino, err := w.getIno(dir)
	if err != nil {
		return err
	}

	w.mu.Lock()
	watch := w.watches.get(ino)
	w.mu.Unlock()
	if watch == nil {
		windows.CloseHandle(ino.handle)
		return fmt.Errorf("%w: %s", ErrNonExistentWatch, pathname)
	}

	if recurse && !watch.recurse {
		windows.CloseHandle(ino.handle)
		return fmt.Errorf("can't use \\... with non-recursive watch %q", pathname)
	}

	err = windows.CloseHandle(ino.handle)
	if err != nil {
		w.sendError(os.NewSyscallError("CloseHandle", err))
	}
	if pathname == dir {
		w.mu.Lock()
		mask := watch.mask
		watch.mask = 0
		w.mu.Unlock()
		w.sendEvent(watch.path, "", mask&sysFSIGNORED)
	} else {
		name := filepath.Base(pathname)
		w.mu.Lock()
		mask := watch.names[name]
		delete(watch.names, name)
		w.mu.Unlock()
		w.sendEvent(filepath.Join(watch.path, name), "", mask&sysFSIGNORED)
	}

	return w.startRead(watch)
}

// Must run within the I/O thread.
func (w *readDirChangesW) deleteWatch(watch *watch) {
	// Snapshot+clear under the lock so concurrent WatchList() readers see a
	// consistent state. sendEvent must run outside the lock since it can
	// block on the user-facing Events channel.
	w.mu.Lock()
	names := watch.names
	watch.names = make(map[string]uint64)
	mask := watch.mask
	watch.mask = 0
	w.mu.Unlock()

	for name, m := range names {
		if m&provisional == 0 {
			w.sendEvent(filepath.Join(watch.path, name), "", m&sysFSIGNORED)
		}
	}
	if mask != 0 && mask&provisional == 0 {
		w.sendEvent(watch.path, "", mask&sysFSIGNORED)
	}
}

// (Re)arms the read for watch, or tears the watch down when nothing on it is
// watched any more.
//
// A read that is still outstanding is cancelled first. CancelIo only requests
// the cancellation: the cancelled op's completion packet arrives later, and
// until then the kernel may still write into its OVERLAPPED and buffer. So the
// cancelled op stays in w.ops until the reader dequeues that packet, and the new
// read gets its own op and its own buffer rather than reusing them.
//
// Must run within the I/O thread.
func (w *readDirChangesW) startRead(watch *watch) error {
	if watch.retired {
		// Torn down already: the handle is closed and may have been reissued
		// to someone else, so it must not be used again.
		return nil
	}
	if watch.cur != nil {
		watch.cur = nil
		err := windows.CancelIo(watch.ino.handle)
		if err != nil {
			w.sendError(os.NewSyscallError("CancelIo", err))
			w.deleteWatch(watch)
		}
	}
	mask := w.toWindowsFlags(watch.mask)
	for _, m := range watch.names {
		mask |= w.toWindowsFlags(m)
	}
	if mask == 0 {
		watch.retired = true
		err := windows.CloseHandle(watch.ino.handle)
		if err != nil {
			w.sendError(os.NewSyscallError("CloseHandle", err))
		}
		w.mu.Lock()
		if w.watches.get(watch.ino) == watch {
			delete(w.watches[watch.ino.volume], watch.ino.index)
		}
		w.mu.Unlock()
		return nil
	}

	op := &readOp{watch: watch, buf: watch.spare}
	watch.spare = nil
	if op.buf == nil {
		op.buf = make([]byte, watch.bufsize)
	}
	// We need to pass the array, rather than the slice.
	rdErr := windows.ReadDirectoryChanges(watch.ino.handle,
		unsafe.SliceData(op.buf), uint32(len(op.buf)),
		watch.recurse, mask, nil, &op.ov, 0)
	if rdErr == nil {
		w.ops[op] = struct{}{}
		watch.cur = op
	} else {
		watch.spare = op.buf // Never issued, so the kernel doesn't hold it.

		err := os.NewSyscallError("ReadDirectoryChanges", rdErr)
		if rdErr == windows.ERROR_ACCESS_DENIED && watch.mask&provisional == 0 {
			// Watched directory was probably removed
			w.sendEvent(watch.path, "", watch.mask&sysFSDELETESELF)
			err = nil
		}
		w.deleteWatch(watch)
		w.startRead(watch)
		return err
	}
	return nil
}

// readEvents reads from the I/O completion port, converts the
// received events into Event objects and sends them via the Events channel.
// Entry point to the I/O thread.
func (w *readDirChangesW) readEvents() {
	var (
		n   uint32
		key uintptr
		ov  *windows.Overlapped
	)
	runtime.LockOSThread()

	for {
		// This error is handled after the ov == nil check below.
		qErr := windows.GetQueuedCompletionStatus(w.port, &n, &key, &ov, windows.INFINITE)

		if ov == nil {
			select {
			case ch := <-w.done:
				w.mu.Lock()
				var indexes []indexMap
				for _, index := range w.watches {
					indexes = append(indexes, index)
				}
				w.mu.Unlock()
				for _, index := range indexes {
					for _, watch := range index {
						w.deleteWatch(watch)
						w.startRead(watch)
					}
				}
				// Every watch is torn down; wait for the cancelled reads to
				// come back before the port goes away.
				w.drainOps()

				err := windows.CloseHandle(w.port)
				if err != nil {
					err = os.NewSyscallError("CloseHandle", err)
				}
				close(w.Events)
				close(w.Errors)
				ch <- err
				return
			case in := <-w.input:
				switch in.op {
				case opAddWatch:
					in.reply <- w.addWatch(in.path, uint64(in.flags), in.bufsize)
				case opRemoveWatch:
					in.reply <- w.remWatch(in.path)
				}
			default:
			}
			continue
		}

		// Every packet with an OVERLAPPED comes from an op in w.ops, which is
		// what has kept it alive until now. Look it up by identity before
		// dereferencing anything.
		op := (*readOp)(unsafe.Pointer(ov))
		if _, ok := w.ops[op]; !ok {
			continue
		}
		delete(w.ops, op)

		watch := op.watch
		if watch.retired {
			// The watch was torn down after this read was issued. Its handle
			// is closed, so nothing here may be acted on.
			continue
		}
		// A read that is no longer the watch's current one was superseded by
		// a re-arm in Add or Remove. Its buffer is still valid and is parsed
		// below, but the watch already has a newer read outstanding, so this
		// one must not re-arm it.
		current := watch.cur == op
		if current {
			watch.cur = nil
		}

		switch qErr {
		case nil:
			// No error
		case windows.ERROR_MORE_DATA:
			// The i/o succeeded but the buffer is full.
			// In theory we should be building up a full packet.
			// In practice we can get away with just carrying on.
			n = uint32(unsafe.Sizeof(op.buf))
		case windows.ERROR_ACCESS_DENIED:
			w.recycle(watch, op)
			if !current {
				continue // The current read reports this too.
			}
			// Watched directory was probably removed
			w.sendEvent(watch.path, "", watch.mask&sysFSDELETESELF)
			w.deleteWatch(watch)
			w.startRead(watch)
			continue
		case windows.ERROR_OPERATION_ABORTED:
			// CancelIo was called on this handle
			w.recycle(watch, op)
			continue
		default:
			w.recycle(watch, op)
			if current {
				w.sendError(os.NewSyscallError("GetQueuedCompletionPort", qErr))
			}
			continue
		}

		var offset uint32
		for {
			if n == 0 {
				w.sendError(ErrEventOverflow)
				break
			}

			// Point "raw" to the event in the buffer
			raw := (*windows.FileNotifyInformation)(unsafe.Pointer(&op.buf[offset]))

			// Create a buf that is the size of the path name
			size := int(raw.FileNameLength / 2)
			buf := unsafe.Slice(&raw.FileName, size)
			name := windows.UTF16ToString(buf)
			fullname := filepath.Join(watch.path, name)

			if debug {
				internal.Debug(fullname, raw.Action)
			}

			var mask uint64
			switch raw.Action {
			case windows.FILE_ACTION_REMOVED:
				mask = sysFSDELETESELF
			case windows.FILE_ACTION_MODIFIED:
				mask = sysFSMODIFY
			case windows.FILE_ACTION_RENAMED_OLD_NAME:
				watch.rename = name
			case windows.FILE_ACTION_RENAMED_NEW_NAME:
				// Update saved path of all sub-watches and rename the
				// names entry under the lock so WatchList() can't observe
				// a torn state.
				old := filepath.Join(watch.path, watch.rename)
				w.mu.Lock()
				for _, watchMap := range w.watches {
					for _, ww := range watchMap {
						if hasPathPrefix(ww.path, old) {
							ww.path = filepath.Join(fullname, strings.TrimPrefix(ww.path, old))
						}
					}
				}
				if watch.names[watch.rename] != 0 {
					watch.names[name] |= watch.names[watch.rename]
					delete(watch.names, watch.rename)
					mask = sysFSMOVESELF
				}
				w.mu.Unlock()
			}

			if raw.Action != windows.FILE_ACTION_RENAMED_NEW_NAME {
				w.sendEvent(fullname, "", watch.names[name]&mask)
			}
			if raw.Action == windows.FILE_ACTION_REMOVED {
				w.mu.Lock()
				ignored := watch.names[name] & sysFSIGNORED
				delete(watch.names, name)
				w.mu.Unlock()
				w.sendEvent(fullname, "", ignored)
			}

			if watch.rename != "" && raw.Action == windows.FILE_ACTION_RENAMED_NEW_NAME {
				w.sendEvent(fullname, filepath.Join(watch.path, watch.rename), watch.mask&w.toFSnotifyFlags(raw.Action))
			} else {
				w.sendEvent(fullname, "", watch.mask&w.toFSnotifyFlags(raw.Action))
			}

			if raw.Action == windows.FILE_ACTION_RENAMED_NEW_NAME {
				w.sendEvent(filepath.Join(watch.path, watch.rename), "", watch.names[name]&mask)
			}

			// Move to the next event in the buffer
			if raw.NextEntryOffset == 0 {
				break
			}
			offset += raw.NextEntryOffset

			// Error!
			if offset >= n {
				//lint:ignore ST1005 Windows should be capitalized
				w.sendError(errors.New("Windows system assumed buffer larger than it is, events have likely been missed"))
				break
			}
		}

		w.recycle(watch, op)
		if !current || watch.retired {
			// Superseded, or the events above tore the watch down.
			continue
		}
		if err := w.startRead(watch); err != nil {
			w.sendError(err)
		}
	}
}

// recycle keeps the buffer of a completed op for the watch's next read. The
// kernel is done with it once the op's completion packet has been dequeued.
//
// Must run within the I/O thread.
func (w *readDirChangesW) recycle(watch *watch, op *readOp) {
	if !watch.retired && watch.spare == nil {
		watch.spare = op.buf
	}
}

// drainOps waits for the completion packets of every outstanding read, so no
// op is released while the kernel may still write into it. Close calls it
// after every watch handle has been closed, which cancels their reads, so the
// packets arrive promptly. Ops still outstanding after closeDrainTimeout are
// pinned for the life of the process instead of being released.
//
// Must run within the I/O thread.
func (w *readDirChangesW) drainOps() {
	deadline := time.Now().Add(closeDrainTimeout)
	for len(w.ops) > 0 {
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		var (
			n   uint32
			key uintptr
			ov  *windows.Overlapped
		)
		err := windows.GetQueuedCompletionStatus(w.port, &n, &key, &ov, uint32(wait/time.Millisecond)+1)
		if ov == nil {
			if err == windows.Errno(windows.WAIT_TIMEOUT) {
				break
			}
			continue // A wakeup posted by Add, Remove or Close.
		}
		delete(w.ops, (*readOp)(unsafe.Pointer(ov)))
	}
	if len(w.ops) > 0 {
		abandonedOpsMu.Lock()
		for op := range w.ops {
			abandonedOps = append(abandonedOps, op)
		}
		abandonedOpsMu.Unlock()
		w.ops = make(map[*readOp]struct{})
	}
}

func (w *readDirChangesW) toWindowsFlags(mask uint64) uint32 {
	var m uint32
	if mask&sysFSMODIFY != 0 {
		m |= windows.FILE_NOTIFY_CHANGE_LAST_WRITE
	}
	if mask&(sysFSMOVE|sysFSCREATE|sysFSDELETE) != 0 {
		m |= windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME
	}
	return m
}

func (w *readDirChangesW) toFSnotifyFlags(action uint32) uint64 {
	switch action {
	case windows.FILE_ACTION_ADDED:
		return sysFSCREATE
	case windows.FILE_ACTION_REMOVED:
		return sysFSDELETE
	case windows.FILE_ACTION_MODIFIED:
		return sysFSMODIFY
	case windows.FILE_ACTION_RENAMED_OLD_NAME:
		return sysFSMOVEDFROM
	case windows.FILE_ACTION_RENAMED_NEW_NAME:
		return sysFSMOVEDTO
	}
	return 0
}

func (w *readDirChangesW) xSupports(op Op) bool {
	if op.Has(xUnportableOpen) || op.Has(xUnportableRead) ||
		op.Has(xUnportableCloseWrite) || op.Has(xUnportableCloseRead) {
		return false
	}
	return true
}
