//go:build darwin
// +build darwin

package fsnotify

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// darwinVersion returns version os Darwin (17 is macOS 10.13).
func darwinVersion() (int, error) {
	s, err := unix.Sysctl("kern.osrelease")
	if err != nil {
		return 0, err
	}
	s = strings.Split(s, ".")[0]
	return strconv.Atoi(s)
}

// testExchangedataForWatcher tests the watcher with the exchangedata operation
// on macOS. This is widely used for atomic saves on macOS, e.g. TextMate and in
// Apple's NSDocument.
//
// https://developer.apple.com/library/mac/documentation/Darwin/Reference/ManPages/man2/exchangedata.2.html
// https://github.com/textmate/textmate/blob/cd016be2/Frameworks/io/src/swap_file_data.cc#L20
func testExchangedataForWatcher(t *testing.T, watchDir bool) {
	osVersion, err := darwinVersion()
	if err != nil {
		t.Fatal("unable to get Darwin version:", err)
	}
	if osVersion >= 17 {
		t.Skip("Exchangedata is deprecated in macOS 10.13")
	}

	testDir1 := t.TempDir() // Create directory to watch
	testDir2 := t.TempDir() // For the intermediate file

	resolvedFilename := "TestFsnotifyEvents.file"

	// TextMate does:
	//
	// 1. exchangedata (intermediate, resolved)
	// 2. unlink intermediate
	//
	// Let's try to simulate that:
	resolved := filepath.Join(testDir1, resolvedFilename)
	intermediate := filepath.Join(testDir2, resolvedFilename+"~")

	// Make sure we create the file before we start watching
	createAndSyncFile(t, resolved)

	w := newCollector(t)
	w.collect(t)

	// Test both variants in isolation
	if watchDir {
		addWatch(t, w.w, testDir1)
	} else {
		addWatch(t, w.w, resolved)
	}

	// Repeat to make sure the watched file/directory "survives" the
	// REMOVE/CREATE loop.
	for i := 1; i <= 3; i++ {
		createAndSyncFile(t, intermediate) // intermediate file is created outside the watcher

		if err := unix.Exchangedata(intermediate, resolved, 0); err != nil { // 1. Swap
			t.Fatalf("[%d] exchangedata failed: %s", i, err)
		}
		eventSeparator()
		err := os.Remove(intermediate) // delete the intermediate file
		if err != nil {
			t.Fatalf("[%d] remove %s failed: %s", i, intermediate, err)
		}

		eventSeparator()
	}

	// The events will be (CHMOD + REMOVE + CREATE) X 2. Let's focus on the last two:
	events := w.stop(t)
	var rm, create Events
	for _, e := range events {
		if e.Has(Create) {
			create = append(create, e)
		}
		if e.Has(Remove) {
			rm = append(rm, e)
		}
	}
	if len(rm) < 3 {
		t.Fatalf("less than 3 REMOVE events:\n%s", events)
	}
	if len(create) < 3 {
		t.Fatalf("less than 3 CREATE events:\n%s", events)
	}
}

func createAndSyncFile(t *testing.T, filepath string) {
	f1, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating %s failed: %s", filepath, err)
	}
	f1.Sync()
	f1.Close()
}

// TestExchangedataInWatchedDir test exchangedata operation on file in watched dir.
func TestExchangedataInWatchedDir(t *testing.T) {
	t.Parallel()
	testExchangedataForWatcher(t, true)
}

// TestExchangedataInWatchedDir test exchangedata operation on watched file.
func TestExchangedataInWatchedFile(t *testing.T) {
	t.Parallel()
	testExchangedataForWatcher(t, false)
}
