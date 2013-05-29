// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd openbsd netbsd darwin linux

package fsnotify

import (
	"os"
	"testing"
	"time"
)

func TestFsnotifyFakeSymlink(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"

	// Create directory to watch
	if err := os.Mkdir(testDir, 0777); err != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}
	defer os.RemoveAll(testDir)

	var errorsReceived counter
	// Receive errors on the error channel on a separate goroutine
	go func() {
		for errors := range watcher.Error {
			t.Logf("Received error: %s", errors)
			errorsReceived.increment()
		}
	}()

	// Count the CREATE events received
	var createEventsReceived counter
	var otherEventsReceived counter
	go func() {
		for ev := range watcher.Event {
			t.Logf("event received: %s", ev)
			if ev.IsCreate() {
				createEventsReceived.increment()
			} else {
				otherEventsReceived.increment()
			}
		}
	}()

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	if os.Symlink("_test/zzz", "_test/zzznew") != nil {
		t.Fatalf("Failed to create bogus symlink: %s", err)
	}
	t.Logf("Created bogus symlink")

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)

	// Should not be error, just no events for broken links (watching nothing)
	if errorsReceived.value() > 0 {
		t.Fatal("fsnotify errors have been received.")
	}
	if otherEventsReceived.value() > 0 {
		t.Fatal("fsnotify other events received on the broken link")
	}

	// Except for 1 create event (for the link itself)
	if createEventsReceived.value() == 0 {
		t.Fatal("fsnotify create events were not received after 500 ms")
	}
	if createEventsReceived.value() > 1 {
		t.Fatal("fsnotify more create events received than expected")
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
}
