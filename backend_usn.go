//go:build windows && usn

// Windows backend based on USN Journal
//
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-usn_journal_data_v0
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-usn_record_v2

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows API constants for USN operations
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ni-winioctl-fsctl_query_usn_journal
const (
	FSCTL_QUERY_USN_JOURNAL = 0x000900F4
	FSCTL_READ_USN_JOURNAL  = 0x000900BB

	MAX_RECORD_BUFFER_SIZE = 65536

	USN_REASON_FILE_CREATE     = 0x00000100
	USN_REASON_FILE_DELETE     = 0x00000200
	USN_REASON_RENAME_NEW_NAME = 0x00002000
	USN_REASON_DATA_OVERWRITE  = 0x00000001
	USN_REASON_DATA_EXTEND     = 0x00000002
	USN_REASON_DATA_TRUNCATION = 0x00000004
)

type FILE_ID_INFO struct {
	VolumeSerialNumber uint64
	FileId             [16]byte // This contains the file reference number
}

// USN_RECORD_V4 represents the latest USN record format
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-usn_record_v4
type USN_RECORD_V4 struct {
	RecordLength              uint32
	MajorVersion              uint16
	MinorVersion              uint16
	FileReferenceNumber       uint64
	ParentFileReferenceNumber uint64
	Usn                       int64
	TimeStamp                 int64
	Reason                    uint32
	SourceInfo                uint32
	SecurityId                uint32
	FileAttributes            uint32
	FileNameLength            uint16
	FileNameOffset            uint16
	// FileName is parsed separately as it's a variable length field
}

// CREATE_USN_JOURNAL_DATA is used to create a USN journal
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-create_usn_journal_data
type CREATE_USN_JOURNAL_DATA struct {
	MaximumSize     uint64
	AllocationDelta uint64
}

// QUERY_USN_JOURNAL_DATA is used to query a USN journal
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-query_usn_journal_data
type QUERY_USN_JOURNAL_DATA struct {
	UsnJournalID    uint64
	FirstUsn        int64
	NextUsn         int64
	LowestValidUsn  int64
	MaxUsn          int64
	MaximumSize     uint64
	AllocationDelta uint64
}

// READ_USN_JOURNAL_DATA is used to read from a USN journal
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-read_usn_journal_data
type READ_USN_JOURNAL_DATA struct {
	StartUsn          int64
	ReasonMask        uint32
	ReturnOnlyOnClose uint32
	Timeout           uint64
	BytesToWaitFor    uint64
	UsnJournalID      uint64
}

// DELETE_USN_JOURNAL_DATA is used to delete a USN journal
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-delete_usn_journal_data
type DELETE_USN_JOURNAL_DATA struct {
	UsnJournalID uint64
	DeleteFlags  uint32
}

// USN_JOURNAL_DATA_V1 provides information about a USN journal
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-usn_journal_data_v1
type USN_JOURNAL_DATA_V1 struct {
	UsnJournalID             uint64
	FirstUsn                 int64
	NextUsn                  int64
	LowestValidUsn           int64
	MaxUsn                   int64
	MaximumSize              uint64
	AllocationDelta          uint64
	MinSupportedMajorVersion uint16
	MaxSupportedMajorVersion uint16
}

// MFT_ENUM_DATA_V0 is used to enumerate MFT records
// https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ns-winioctl-mft_enum_data_v0
type MFT_ENUM_DATA_V0 struct {
	StartFileReferenceNumber uint64
	LowUsn                   int64
	HighUsn                  int64
}

// FileEvent represents a file change event
type FileEvent struct {
	Path      string
	Operation string
	Reason    uint32
	Time      int64
}

// usnBackend represents a USN change journal watcher
type usnBackend struct {
	*shared
	Events chan Event
	Errors chan error

	paths   map[string]bool
	volumes map[string]volumeInfo
	wg      sync.WaitGroup

	closed bool
}

// volumeInfo holds information about a monitored volume
type volumeInfo struct {
	handle        windows.Handle
	rootPath      string
	journalID     uint64
	nextUSN       int64
	fileRefToPath map[uint64]string
}

func newBackend(ev chan Event, errs chan error) (backend, error) {
	u := &usnBackend{
		shared:  newShared(ev, errs),
		Events:  ev,
		Errors:  errs,
		paths:   make(map[string]bool),
		volumes: make(map[string]volumeInfo),
	}
	return u, nil
}

func (u *usnBackend) Add(path string) error {
	return u.AddWith(path)
}

func (u *usnBackend) AddWith(path string, opts ...addOpt) error {
	if u.isClosed() {
		return ErrClosed
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), filepath.ToSlash(path))
	}

	with := getOptions(opts...)
	if !u.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// Already being watched
	if u.paths[absPath] {
		return nil
	}

	// Check if path exists
	if _, err := os.Stat(absPath); err != nil {
		return err
	}

	u.paths[absPath] = true

	// Get volume root from path
	volumeName := filepath.VolumeName(absPath)
	if volumeName == "" {
		// Try to get volume from current directory if not found
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		volumeName = filepath.VolumeName(cwd)
		if volumeName == "" {
			return errors.New("could not determine volume for path")
		}
	}

	// Check if we're already watching this volume
	if _, exists := u.volumes[volumeName]; !exists {
		err := u.setupVolumeMonitoring(volumeName)
		if err != nil {
			return err
		}
	}

	if err := buildFileReferenceMap(u.volumes[volumeName].fileRefToPath, filepath.Dir(absPath)); err != nil {
		return fmt.Errorf("could not build file reference map: %w", err)
	}

	return nil
}

func (u *usnBackend) setupVolumeMonitoring(volumePath string) error {
	// Open a handle to the volume
	fmt.Println(fmt.Sprintf(`\\.\%s`, volumePath))
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(fmt.Sprintf(`\\.\%s`, volumePath)),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to open volume %s: %w", volumePath, err)
	}

	// Query the USN journal to get the journal ID and next USN
	var queryData QUERY_USN_JOURNAL_DATA
	var bytesReturned uint32
	err = windows.DeviceIoControl(
		handle,
		FSCTL_QUERY_USN_JOURNAL,
		nil,
		0,
		(*byte)(unsafe.Pointer(&queryData)),
		uint32(unsafe.Sizeof(queryData)),
		&bytesReturned,
		nil,
	)
	if err != nil {
		windows.CloseHandle(handle)
		return fmt.Errorf("failed to query USN journal: %w", err)
	}

	// Store volume information
	volInfo := volumeInfo{
		handle:        handle,
		rootPath:      volumePath,
		journalID:     queryData.UsnJournalID,
		nextUSN:       queryData.NextUsn,
		fileRefToPath: make(map[uint64]string),
	}
	u.volumes[volumePath] = volInfo

	// Start monitoring this volume
	u.wg.Add(1)
	go u.monitorVolume(volumePath)

	return nil
}

func buildFileReferenceMap(refMap map[uint64]string, rootPath string) error {
	return filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		refNum, err := getFileReferenceNumber(path)
		if err != nil {
			// Log error but continue processing other files
			return nil
		}

		refMap[refNum] = path
		return nil
	})
}

func getFileReferenceNumber(path string) (uint64, error) {
	// Open the file with read attributes access
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, // Required for directories
		0,
	)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)

	// Get the file ID info
	var fileInfo FILE_ID_INFO
	err = windows.GetFileInformationByHandleEx(
		handle,
		windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&fileInfo)),
		uint32(unsafe.Sizeof(fileInfo)),
	)
	if err != nil {
		return 0, err
	}

	// The first 8 bytes of FileId contain the file reference number
	fileRefNum := *(*uint64)(unsafe.Pointer(&fileInfo.FileId[0]))
	return fileRefNum, nil
}

// monitorVolume continuously monitors a volume for changes
func (u *usnBackend) monitorVolume(volumePath string) {
	defer u.wg.Done()

	u.mu.Lock()
	volInfo := u.volumes[volumePath]
	u.mu.Unlock()

	buffer := make([]byte, MAX_RECORD_BUFFER_SIZE)

	numErrors := 0

	for {
		select {
		case <-u.done:
			return
		default:
			// Continue monitoring
		}

		// Set up the read command
		readData := READ_USN_JOURNAL_DATA{
			StartUsn:          volInfo.nextUSN,
			ReasonMask:        USN_REASON_FILE_CREATE | USN_REASON_FILE_DELETE | USN_REASON_RENAME_NEW_NAME | USN_REASON_DATA_OVERWRITE | USN_REASON_DATA_EXTEND | USN_REASON_DATA_TRUNCATION,
			ReturnOnlyOnClose: 0,
			Timeout:           0,
			BytesToWaitFor:    0,
			UsnJournalID:      volInfo.journalID,
		}

		var bytesReturned uint32
		err := windows.DeviceIoControl(
			volInfo.handle,
			FSCTL_READ_USN_JOURNAL,
			(*byte)(unsafe.Pointer(&readData)),
			uint32(unsafe.Sizeof(readData)),
			&buffer[0],
			uint32(len(buffer)),
			&bytesReturned,
			nil,
		)

		if err != nil {
			numErrors++
			if !errors.Is(err, windows.ERROR_HANDLE_EOF) {
				u.sendError(os.NewSyscallError("DeviceIoControl(FSCTL_READ_USN_JOURNAL)",
					fmt.Errorf("error reading USN journal(%s): %w", volumePath, err)))
			}
			// TODO: make this configurable?
			if numErrors > 5 {
				u.sendError(errors.New("too many errors, aborting"))
				return
			}
			continue
		}

		numErrors = 0

		if bytesReturned <= 8 {
			// No new records, wait for next tick
			continue
		}

		// Update the next USN
		nextUSN := *(*int64)(unsafe.Pointer(&buffer[0]))

		u.mu.Lock()
		volInfo.nextUSN = nextUSN
		u.volumes[volumePath] = volInfo
		u.mu.Unlock()

		// Process the records
		u.processRecords(volumePath, buffer[8:bytesReturned])
	}
}

// processRecords processes USN records from the buffer
func (u *usnBackend) processRecords(volumePath string, buffer []byte) {
	var offset uint32 = 0

	for offset < uint32(len(buffer)) {
		if offset+8 > uint32(len(buffer)) {
			break // Not enough data for a complete record
		}

		record := (*USN_RECORD_V4)(unsafe.Pointer(&buffer[offset]))
		if record.RecordLength == 0 || offset+record.RecordLength > uint32(len(buffer)) {
			break // Invalid record or not enough data
		}

		// Extract file name
		nameOffset := offset + uint32(record.FileNameOffset)
		if nameOffset+uint32(record.FileNameLength) > uint32(len(buffer)) {
			break // Not enough data for the filename
		}

		nameBytes := buffer[nameOffset : nameOffset+uint32(record.FileNameLength)]

		// Convert UTF-16LE to string
		name := windows.UTF16ToString((*[1024]uint16)(unsafe.Pointer(&nameBytes[0]))[:record.FileNameLength/2])

		u.mu.Lock()
		// Get parent path
		parentPath := ""
		if parentName, ok := u.volumes[volumePath].fileRefToPath[record.ParentFileReferenceNumber]; ok {
			parentPath = parentName
		}

		// Build full path
		fullPath := filepath.Join(parentPath, name)

		// Store this file's reference
		u.volumes[volumePath].fileRefToPath[record.FileReferenceNumber] = filepath.Join(parentPath, name)
		u.mu.Unlock()

		// Determine operation type
		var op Op
		switch {
		case record.Reason&USN_REASON_FILE_CREATE != 0:
			op |= Create
		case record.Reason&USN_REASON_FILE_DELETE != 0:
			op |= Remove
		case record.Reason&USN_REASON_RENAME_NEW_NAME != 0:
			op |= Rename
		case record.Reason&(USN_REASON_DATA_OVERWRITE|USN_REASON_DATA_EXTEND|USN_REASON_DATA_TRUNCATION) != 0:
			op |= Write
		}
		// Only sendEvent if file is in watch list
		u.mu.Lock()
		for watchPath := range u.paths {
			if strings.HasPrefix(fullPath, watchPath) || filepath.Dir(fullPath) == watchPath {
				if debug {
					fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  %s → %q\n",
						time.Now().Format("15:04:05.000000000"), op, fullPath)
				}
				u.sendEvent(Event{Name: fullPath, Op: op})
				break
			}
		}
		u.mu.Unlock()

		// Move to next record
		offset += uint32(record.RecordLength)
	}
}

func (u *usnBackend) Remove(path string) error {
	if u.isClosed() {
		return nil
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), filepath.ToSlash(path))
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	u.mu.Lock()
	delete(u.paths, absPath)
	u.mu.Unlock()
	return nil
}

func (u *usnBackend) WatchList() []string {
	if u.isClosed() {
		return nil
	}

	l := make([]string, 0)
	for k := range u.paths {
		l = append(l, k)
	}
	return l
}

func (u *usnBackend) Close() error {
	if w.shared.close() {
		return nil
	}

	u.wg.Wait() // Wait for all goroutines to finish

	u.mu.Lock()
	defer u.mu.Unlock()

	// Close all volume handles
	for _, vol := range u.volumes {
		windows.CloseHandle(vol.handle)
	}

	// Clear the volumes map
	u.volumes = make(map[string]volumeInfo)
	u.paths = make(map[string]bool)
	return nil
}

func (u *usnBackend) xSupports(op Op) bool {
	if op.Has(xUnportableOpen) || op.Has(xUnportableRead) ||
		op.Has(xUnportableCloseWrite) || op.Has(xUnportableCloseRead) {
		return false
	}
	return true
}
