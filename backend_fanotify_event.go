//go:build linux && !appengine
// +build linux,!appengine

package fsnotify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"unsafe"

	"github.com/syndtr/gocapability/capability"
	"golang.org/x/sys/unix"
)

const (
	sizeOfFanotifyEventMetadata = uint32(unsafe.Sizeof(unix.FanotifyEventMetadata{}))
)

// These fanotify structs are not defined in golang.org/x/sys/unix
type fanotifyEventInfoHeader struct {
	InfoType uint8
	pad      uint8
	Len      uint16
}

type kernelFSID struct {
	val [2]int32
}

// FanotifyEventInfoFID represents a unique file identifier info record.
// This structure is used for records of types FAN_EVENT_INFO_TYPE_FID,
// FAN_EVENT_INFO_TYPE_DFID and FAN_EVENT_INFO_TYPE_DFID_NAME.
// For FAN_EVENT_INFO_TYPE_DFID_NAME there is additionally a null terminated
// name immediately after the file handle.
type fanotifyEventInfoFID struct {
	Header     fanotifyEventInfoHeader
	fsid       kernelFSID
	fileHandle byte
}

// returns major, minor, patch version of the kernel
// upon error the string values are empty and the error
// indicates the reason for failure
func kernelVersion() (maj, min, patch int, err error) {
	var sysinfo unix.Utsname
	err = unix.Uname(&sysinfo)
	if err != nil {
		return
	}
	re := regexp.MustCompile(`([0-9]+)`)
	version := re.FindAllString(string(sysinfo.Release[:]), -1)
	if maj, err = strconv.Atoi(version[0]); err != nil {
		return
	}
	if min, err = strconv.Atoi(version[1]); err != nil {
		return
	}
	if patch, err = strconv.Atoi(version[2]); err != nil {
		return
	}
	return maj, min, patch, nil
}

// return true if process has CAP_SYS_ADMIN privilege
// else return false
func checkCapSysAdmin() (bool, error) {
	capabilities, err := capability.NewPid2(os.Getpid())
	if err != nil {
		return false, err
	}
	capabilities.Load()
	capSysAdmin := capabilities.Get(capability.EFFECTIVE, capability.CAP_SYS_ADMIN)
	return capSysAdmin, nil
}

func flagsValid(flags uint) error {
	isSet := func(n, k uint) bool {
		return n&k == k
	}
	if isSet(flags, unix.FAN_REPORT_FID|unix.FAN_CLASS_CONTENT) {
		return errors.New("FAN_REPORT_FID cannot be set with FAN_CLASS_CONTENT")
	}
	if isSet(flags, unix.FAN_REPORT_FID|unix.FAN_CLASS_PRE_CONTENT) {
		return errors.New("FAN_REPORT_FID cannot be set with FAN_CLASS_PRE_CONTENT")
	}
	if isSet(flags, unix.FAN_REPORT_NAME) {
		if !isSet(flags, unix.FAN_REPORT_DIR_FID) {
			return errors.New("FAN_REPORT_NAME must be set with FAN_REPORT_DIR_FID")
		}
	}
	return nil
}

func isFanotifyMarkMaskValid(flags uint, mask uint64) error {
	isSet := func(n, k uint64) bool {
		return n&k == k
	}
	if isSet(uint64(flags), unix.FAN_MARK_MOUNT) {
		if isSet(mask, unix.FAN_CREATE) ||
			isSet(mask, unix.FAN_ATTRIB) ||
			isSet(mask, unix.FAN_MOVE) ||
			isSet(mask, unix.FAN_DELETE_SELF) ||
			isSet(mask, unix.FAN_DELETE) {
			return errors.New("mountpoint cannot be watched for create, attrib, move or delete self event types")
		}
	}
	return nil
}

func checkMask(mask uint64, validEventTypes []EventType) error {
	flags := mask
	for _, v := range validEventTypes {
		if flags&uint64(v) == uint64(v) {
			flags = flags ^ uint64(v)
		}
	}
	if flags != 0 {
		return ErrInvalidFlagCombination
	}
	return nil
}

// Check if specified fanotify_init flags are supported for the given
// kernel version. If none of the defined flags are specified
// then the basic option works on any kernel version.
func fanotifyInitFlagsKernelSupport(flags uint, maj, min int) bool {
	type kernelVersion struct {
		maj int
		min int
	}
	// fanotify init flags
	var flagPerKernelVersion = map[uint]kernelVersion{
		unix.FAN_ENABLE_AUDIT:     {4, 15},
		unix.FAN_REPORT_FID:       {5, 1},
		unix.FAN_REPORT_DIR_FID:   {5, 9},
		unix.FAN_REPORT_NAME:      {5, 9},
		unix.FAN_REPORT_DFID_NAME: {5, 9},
	}

	check := func(n, k uint, w, x int) (bool, error) {
		if n&k == k {
			if maj > w {
				return true, nil
			} else if maj == w && min >= x {
				return true, nil
			}
			return false, nil
		}
		return false, errors.New("flag not set")
	}
	for flag, ver := range flagPerKernelVersion {
		if v, err := check(flags, flag, ver.maj, ver.min); err != nil {
			continue // flag not set; check other flags
		} else {
			return v
		}
	}
	// if none of these flags were specified then the basic option
	// works on any kernel version
	return true
}

// Check if specified fanotify_mark flags are supported for the given
// kernel version. If none of the defined flags are specified
// then the basic option works on any kernel version.
func fanotifyMarkFlagsKernelSupport(flags uint64, maj, min int) bool {
	type kernelVersion struct {
		maj int
		min int
	}
	// fanotify mark flags
	var fanotifyMarkFlags = map[uint64]kernelVersion{
		unix.FAN_OPEN_EXEC:   {5, 0},
		unix.FAN_ATTRIB:      {5, 1},
		unix.FAN_CREATE:      {5, 1},
		unix.FAN_DELETE:      {5, 1},
		unix.FAN_DELETE_SELF: {5, 1},
		unix.FAN_MOVED_FROM:  {5, 1},
		unix.FAN_MOVED_TO:    {5, 1},
	}

	check := func(n, k uint64, w, x int) (bool, error) {
		if n&k == k {
			if maj > w {
				return true, nil
			} else if maj == w && min >= x {
				return true, nil
			}
			return false, nil
		}
		return false, errors.New("flag not set")
	}
	for flag, ver := range fanotifyMarkFlags {
		if v, err := check(flags, flag, ver.maj, ver.min); err != nil {
			continue // flag not set; check other flags
		} else {
			return v
		}
	}
	// if none of these flags were specified then the basic option
	// works on any kernel version
	return true
}

func fanotifyEventOK(meta *unix.FanotifyEventMetadata, n int) bool {
	return (n >= int(sizeOfFanotifyEventMetadata) &&
		meta.Event_len >= sizeOfFanotifyEventMetadata &&
		int(meta.Event_len) <= n)
}

// permissionType is ignored when isNotificationListener is true.
func newListener(mountpointPath string, entireMount bool, notificationOnly bool, permissionType PermissionType) (*Listener, error) {

	var flags, eventFlags uint

	maj, min, _, err := kernelVersion()
	if err != nil {
		return nil, err
	}
	if !notificationOnly {
		// permission + notification events; cannot have FID with this.
		switch permissionType {
		case PreContent:
			flags = unix.FAN_CLASS_PRE_CONTENT | unix.FAN_CLOEXEC
		case PostContent:
			flags = unix.FAN_CLASS_CONTENT | unix.FAN_CLOEXEC
		default:
			return nil, os.ErrInvalid
		}
	} else {
		switch {
		case maj < 5:
			flags = unix.FAN_CLASS_NOTIF | unix.FAN_CLOEXEC
		case maj == 5:
			if min < 1 {
				flags = unix.FAN_CLASS_NOTIF | unix.FAN_CLOEXEC
			}
			if min >= 1 && min < 9 {
				flags = unix.FAN_CLASS_NOTIF | unix.FAN_CLOEXEC | unix.FAN_REPORT_FID
			}
			if min >= 9 {
				flags = unix.FAN_CLASS_NOTIF | unix.FAN_CLOEXEC | unix.FAN_REPORT_DIR_FID | unix.FAN_REPORT_NAME
			}
		case maj > 5:
			flags = unix.FAN_CLASS_NOTIF | unix.FAN_CLOEXEC | unix.FAN_REPORT_DIR_FID | unix.FAN_REPORT_NAME
		}
		// FAN_MARK_MOUNT cannot be specified with FAN_REPORT_FID, FAN_REPORT_DIR_FID, FAN_REPORT_NAME
		if entireMount {
			flags = unix.FAN_CLASS_NOTIF | unix.FAN_CLOEXEC
		}
	}
	eventFlags = unix.O_RDONLY | unix.O_LARGEFILE | unix.O_CLOEXEC
	if err := flagsValid(flags); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFlagCombination, err)
	}
	if !fanotifyInitFlagsKernelSupport(flags, maj, min) {
		panic("some of the flags specified are not supported on the current kernel; refer to the documentation")
	}
	fd, err := unix.FanotifyInit(flags, eventFlags)
	if err != nil {
		return nil, err
	}
	mountpoint, err := os.Open(mountpointPath)
	if err != nil {
		return nil, fmt.Errorf("error opening mount point %s: %w", mountpointPath, err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("cannot create stopper pipe: %v", err)
	}
	rfdFlags, err := unix.FcntlInt(r.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return nil, fmt.Errorf("stopper error: cannot read fd flags: %v", err)
	}
	_, err = unix.FcntlInt(r.Fd(), unix.F_SETFL, rfdFlags|unix.O_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("stopper error: cannot set fd to non-blocking: %v", err)
	}
	listener := &Listener{
		fd:                 fd,
		flags:              flags,
		mountpoint:         mountpoint,
		kernelMajorVersion: maj,
		kernelMinorVersion: min,
		entireMount:        entireMount,
		notificationOnly:   notificationOnly,
		watches:            make(map[string]bool),
		stopper: struct {
			r *os.File
			w *os.File
		}{r, w},
		Events:           make(chan FanotifyEvent, 4096),
		PermissionEvents: make(chan FanotifyEvent, 4096),
	}
	return listener, nil
}

func (l *Listener) fanotifyMark(path string, flags uint, mask uint64) error {
	if l == nil {
		panic("nil listener")
	}
	skip := true
	if !fanotifyMarkFlagsKernelSupport(mask, l.kernelMajorVersion, l.kernelMinorVersion) {
		panic("some of the mark mask combinations specified are not supported on the current kernel; refer to the documentation")
	}
	if err := isFanotifyMarkMaskValid(flags, mask); err != nil {
		return fmt.Errorf("%v: %w", err, ErrInvalidFlagCombination)
	}
	remove := flags&unix.FAN_MARK_REMOVE == unix.FAN_MARK_REMOVE
	_, found := l.watches[path]
	if found {
		if remove {
			delete(l.watches, path)
			skip = false
		}
	} else {
		if !remove {
			l.watches[path] = true
			skip = false
		}
	}
	if !skip {
		if err := unix.FanotifyMark(l.fd, flags, mask, -1, path); err != nil {
			return err
		}
	}
	return nil
}

func getFileHandle(metadataLen uint16, buf []byte, i int) *unix.FileHandle {
	var fhSize uint32 // this is unsigned int handle_bytes; but Go uses uint32
	var fhType int32  // this is int handle_type; but Go uses int32

	sizeOfFanotifyEventInfoHeader := uint32(unsafe.Sizeof(fanotifyEventInfoHeader{}))
	sizeOfKernelFSIDType := uint32(unsafe.Sizeof(kernelFSID{}))
	sizeOfUint32 := uint32(unsafe.Sizeof(fhSize))
	j := uint32(i) + uint32(metadataLen) + sizeOfFanotifyEventInfoHeader + sizeOfKernelFSIDType
	binary.Read(bytes.NewReader(buf[j:j+sizeOfUint32]), binary.LittleEndian, &fhSize)
	j += sizeOfUint32
	binary.Read(bytes.NewReader(buf[j:j+sizeOfUint32]), binary.LittleEndian, &fhType)
	j += sizeOfUint32
	handle := unix.NewFileHandle(fhType, buf[j:j+fhSize])
	return &handle
}

func getFileHandleWithName(metadataLen uint16, buf []byte, i int) (*unix.FileHandle, string) {
	var fhSize uint32
	var fhType int32
	var fname string
	var nameBytes bytes.Buffer

	sizeOfFanotifyEventInfoHeader := uint32(unsafe.Sizeof(fanotifyEventInfoHeader{}))
	sizeOfKernelFSIDType := uint32(unsafe.Sizeof(kernelFSID{}))
	sizeOfUint32 := uint32(unsafe.Sizeof(fhSize))
	j := uint32(i) + uint32(metadataLen) + sizeOfFanotifyEventInfoHeader + sizeOfKernelFSIDType
	binary.Read(bytes.NewReader(buf[j:j+sizeOfUint32]), binary.LittleEndian, &fhSize)
	j += sizeOfUint32
	binary.Read(bytes.NewReader(buf[j:j+sizeOfUint32]), binary.LittleEndian, &fhType)
	j += sizeOfUint32
	handle := unix.NewFileHandle(fhType, buf[j:j+fhSize])
	j += fhSize
	// stop when NULL byte is read to get the filename
	for i := j; i < j+unix.NAME_MAX; i++ {
		if buf[i] == 0 {
			break
		}
		nameBytes.WriteByte(buf[i])
	}
	if nameBytes.Len() != 0 {
		fname = nameBytes.String()
	}
	return &handle, fname
}

func (l *Listener) readEvents() error {
	var fid *fanotifyEventInfoFID
	var metadata *unix.FanotifyEventMetadata
	var buf [4096 * sizeOfFanotifyEventMetadata]byte
	var name [unix.PathMax]byte
	var fileHandle *unix.FileHandle
	var fileName string

	for {
		n, err := unix.Read(l.fd, buf[:])
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 || n < int(sizeOfFanotifyEventMetadata) {
			break
		}
		i := 0
		metadata = (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[i]))
		for fanotifyEventOK(metadata, n) {
			if metadata.Vers != unix.FANOTIFY_METADATA_VERSION {
				panic("metadata structure from the kernel does not match the structure definition at compile time")
			}
			if metadata.Fd != unix.FAN_NOFD {
				// no fid (applicable to kernels 5.0 and earlier)
				procFdPath := fmt.Sprintf("/proc/self/fd/%d", metadata.Fd)
				n1, err := unix.Readlink(procFdPath, name[:])
				if err != nil {
					i += int(metadata.Event_len)
					n -= int(metadata.Event_len)
					metadata = (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[i]))
					continue
				}
				mask := metadata.Mask
				if mask&unix.FAN_ONDIR == unix.FAN_ONDIR {
					mask = mask ^ unix.FAN_ONDIR
				}
				event := FanotifyEvent{
					Fd:         int(metadata.Fd),
					Path:       string(name[:n1]),
					EventTypes: EventType(mask),
					Pid:        int(metadata.Pid),
				}
				if mask&unix.FAN_ACCESS_PERM == unix.FAN_ACCESS_PERM ||
					mask&unix.FAN_OPEN_PERM == unix.FAN_OPEN_PERM ||
					mask&unix.FAN_OPEN_EXEC_PERM == unix.FAN_OPEN_EXEC_PERM {
					l.PermissionEvents <- event
				} else {
					l.Events <- event
				}
				i += int(metadata.Event_len)
				n -= int(metadata.Event_len)
				metadata = (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[i]))
			} else {
				// fid (applicable to kernels 5.1+)
				fid = (*fanotifyEventInfoFID)(unsafe.Pointer(&buf[i+int(metadata.Metadata_len)]))
				withName := false
				switch {
				case fid.Header.InfoType == unix.FAN_EVENT_INFO_TYPE_FID:
					withName = false
				case fid.Header.InfoType == unix.FAN_EVENT_INFO_TYPE_DFID:
					withName = false
				case fid.Header.InfoType == unix.FAN_EVENT_INFO_TYPE_DFID_NAME:
					withName = true
				default:
					i += int(metadata.Event_len)
					n -= int(metadata.Event_len)
					metadata = (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[i]))
					continue
				}
				if withName {
					fileHandle, fileName = getFileHandleWithName(metadata.Metadata_len, buf[:], i)
					i += len(fileName) // advance some to cover the filename
				} else {
					fileHandle = getFileHandle(metadata.Metadata_len, buf[:], i)
				}
				fd, errno := unix.OpenByHandleAt(int(l.mountpoint.Fd()), *fileHandle, unix.O_RDONLY)
				if errno != nil {
					// log.Println("OpenByHandleAt:", errno)
					i += int(metadata.Event_len)
					n -= int(metadata.Event_len)
					metadata = (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[i]))
					continue
				}
				fdPath := fmt.Sprintf("/proc/self/fd/%d", fd)
				n1, _ := unix.Readlink(fdPath, name[:]) // TODO handle err case
				pathName := string(name[:n1])
				mask := metadata.Mask
				if mask&unix.FAN_ONDIR == unix.FAN_ONDIR {
					mask = mask ^ unix.FAN_ONDIR
				}
				event := FanotifyEvent{
					Fd:         fd,
					Path:       pathName,
					FileName:   fileName,
					EventTypes: EventType(mask),
					Pid:        int(metadata.Pid),
				}
				// As of the kernel release (6.0) permission events cannot have FID flags.
				// So the event here is always a notification event
				l.Events <- event
				i += int(metadata.Event_len)
				n -= int(metadata.Event_len)
				metadata = (*unix.FanotifyEventMetadata)(unsafe.Pointer(&buf[i]))
			}
		}
	}
	return nil
}
