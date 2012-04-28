// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd openbsd netbsd darwin linux

package fsnotify

import (
	"os"
	"testing"
)

func TestFsnotifyFakeSymlink(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"

	// Create directory to watch
	if os.Mkdir(testDir, 0777) != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}
	defer os.RemoveAll(testDir)

	if os.Symlink("_test/zzz", "_test/zzznew") != nil {
		t.Fatalf("Failed to create bogus symlink: %s", err)
	}
	t.Logf("Created bogus symlink")

	var errorsReceived = 0
	// Receive errors on the error channel on a separate goroutine
	go func() {
		for errors := range watcher.Error {
			t.Logf("Received error: %s", errors)
			errorsReceived++
		}
	}()

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	// Should not be error, just no events for broken links (watching nothing)
	if errorsReceived > 0 {
		t.Fatal("fsnotify errors have been received.")
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
}
