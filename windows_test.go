// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package fsnotify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsRemWatch(t *testing.T) {
	// Create directory to watch
	testDir := tempMkdir(t)
	defer os.RemoveAll(testDir)

	// Create a file before watching directory
	testFileAlreadyExists := filepath.Join(testDir, "TestFsnotifyEventsExisting.testfile")
	{
		var f *os.File
		f, err := os.OpenFile(testFileAlreadyExists, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			t.Fatalf("creating test file failed: %s", err)
		}
		f.Sync()
		f.Close()
	}

	watcher := newWatcher(t)
	defer watcher.Close()

	addWatch(t, watcher, testDir)
	if err := watcher.Remove(testDir); err != nil {
		t.Fatalf("Could not remove the watch: %v\n", err)
	}
	if err := watcher.remWatch(testDir); err == nil {
		t.Fatal("Should be fail with closed handle\n")
	}
}
