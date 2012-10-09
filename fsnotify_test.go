// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsnotify

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestFsnotifyMultipleOperations(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Error {
			t.Fatalf("error received: %s", err)
		}
	}()

	const testDir string = "_test"
	const testDirToMoveFiles string = "_test2"

	// Create directory to watch
	if os.Mkdir(testDir, 0777) != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}
	defer os.RemoveAll(testDir)

	// Create directory to that's not watched
	if os.Mkdir(testDirToMoveFiles, 0777) != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}
	defer os.RemoveAll(testDirToMoveFiles)

	const testFile string = "_test/TestFsnotifySeq.testfile"
	const testFileRenamed string = "_test2/TestFsnotifySeqRename.testfile"

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}
	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var createReceived = 0
	var modifyReceived = 0
	var deleteReceived = 0
	var renameReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile {
				t.Logf("event received: %s", event)
				if event.IsDelete() {
					deleteReceived++
				}
				if event.IsModify() {
					modifyReceived++
				}
				if event.IsCreate() {
					createReceived++
				}
				if event.IsRename() {
					renameReceived++
				}
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Create a file
	// This should add at least one event to the fsnotify event queue
	var f *os.File
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	time.Sleep(time.Millisecond)
	f.WriteString("data")
	f.Sync()
	f.Close()

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	cmd := exec.Command("mv", testFile, testFileRenamed)
	err = cmd.Run()
	if err != nil {
		t.Fatalf("rename failed: %s", err)
	}

	// Modify the file outside of the watched dir
	f, err = os.Open(testFileRenamed)
	if err != nil {
		t.Fatalf("open test renamed file failed: %s", err)
	}
	f.WriteString("data")
	f.Sync()
	f.Close()

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	// Recreate the file that was moved
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Close()
	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)
	if createReceived != 2 {
		t.Fatalf("incorrect number of create events received after 500 ms (%d vs %d)", createReceived, 2)
	}
	if modifyReceived != 1 {
		t.Fatalf("incorrect number of modify events received after 500 ms (%d vs %d)", modifyReceived, 1)
	}
	if deleteReceived+renameReceived != 1 {
		t.Fatalf("incorrect number of rename+delete events received after 500 ms (%d vs %d)", renameReceived+deleteReceived, 1)
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
}

func TestFsnotifyMultipleCreates(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Error {
			t.Fatalf("error received: %s", err)
		}
	}()

	const testDir string = "_test"

	// Create directory to watch
	if os.Mkdir(testDir, 0777) != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}
	defer os.RemoveAll(testDir)

	const testFile string = "_test/TestFsnotifySeq.testfile"

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}
	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var createReceived = 0
	var modifyReceived = 0
	var deleteReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile {
				t.Logf("event received: %s", event)
				if event.IsDelete() {
					deleteReceived++
				}
				if event.IsCreate() {
					createReceived++
				}
				if event.IsModify() {
					modifyReceived++
				}
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Create a file
	// This should add at least one event to the fsnotify event queue
	var f *os.File
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	time.Sleep(time.Millisecond)
	f.WriteString("data")
	f.Sync()
	f.Close()

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	os.Remove(testFile)

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	// Recreate the file
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Close()
	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	// Modify
	f, err = os.OpenFile(testFile, os.O_WRONLY, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	time.Sleep(time.Millisecond)
	f.WriteString("data")
	f.Sync()
	f.Close()

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	// Modify
	f, err = os.OpenFile(testFile, os.O_WRONLY, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	time.Sleep(time.Millisecond)
	f.WriteString("data")
	f.Sync()
	f.Close()

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)
	if createReceived != 2 {
		t.Fatalf("incorrect number of create events received after 500 ms (%d vs %d)", createReceived, 2)
	}
	if modifyReceived != 3 {
		t.Fatalf("incorrect number of modify events received after 500 ms (%d vs atleast %d)", modifyReceived, 3)
	}
	if deleteReceived != 1 {
		t.Fatalf("incorrect number of rename+delete events received after 500 ms (%d vs %d)", deleteReceived, 1)
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
}

func TestFsnotifyDirOnly(t *testing.T) {
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

	// Create a file before watching directory
	// This should NOT add any events to the fsnotify event queue
	const testFileAlreadyExists string = "_test/TestFsnotifyEventsExisting.testfile"
	{
		var f *os.File
		f, err = os.OpenFile(testFileAlreadyExists, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			t.Fatalf("creating test file failed: %s", err)
		}
		f.Sync()
		f.Close()
	}

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Error {
			t.Fatalf("error received: %s", err)
		}
	}()

	const testFile string = "_test/TestFsnotifyDirOnly.testfile"

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var createReceived = 0
	var modifyReceived = 0
	var deleteReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile || event.Name == testFileAlreadyExists {
				t.Logf("event received: %s", event)
				if event.IsDelete() {
					deleteReceived++
				}
				if event.IsModify() {
					modifyReceived++
				}
				if event.IsCreate() {
					createReceived++
				}
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Create a file
	// This should add at least one event to the fsnotify event queue
	var f *os.File
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	time.Sleep(time.Millisecond)
	f.WriteString("data")
	f.Sync()
	f.Close()

	time.Sleep(50 * time.Millisecond) // give system time to sync write change before delete

	os.Remove(testFile)
	os.Remove(testFileAlreadyExists)

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)
	if createReceived != 1 {
		t.Fatalf("incorrect number of create events received after 500 ms (%d vs %d)", createReceived, 1)
	}
	if modifyReceived != 1 {
		t.Fatalf("incorrect number of modify events received after 500 ms (%d vs %d)", modifyReceived, 1)
	}
	if deleteReceived != 2 {
		t.Fatalf("incorrect number of delete events received after 500 ms (%d vs %d)", deleteReceived, 2)
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
}

func TestFsnotifySubDir(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"
	const testFile1 string = "_test/TestFsnotifyFile1.testfile"
	const testSubDir string = "_test/sub"
	const testSubDirFile string = "_test/sub/TestFsnotifyFile1.testfile"

	// Create directory to watch
	if os.Mkdir(testDir, 0777) != nil {
		t.Fatalf("Failed to create test directory: %s", err)
	}
	defer os.RemoveAll(testDir)

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Error {
			t.Fatalf("error received: %s", err)
		}
	}()

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var createReceived = 0
	var deleteReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testSubDir || event.Name == testFile1 {
				t.Logf("event received: %s", event)
				if event.IsCreate() {
					createReceived++
				}
				if event.IsDelete() {
					deleteReceived++
				}
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	// Create sub-directory
	if os.Mkdir(testSubDir, 0777) != nil {
		t.Fatalf("Failed to create test sub-directory: %s", err)
	}

	// Create a file
	var f *os.File
	f, err = os.OpenFile(testFile1, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()
	f.Close()

	// Create a file (Should not see this! we are not watching subdir)
	var fs *os.File
	fs, err = os.OpenFile(testSubDirFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	fs.Sync()
	fs.Close()

	// Make sure receive deletes for both file and sub-directory
	os.RemoveAll(testSubDir)
	os.Remove(testFile1)

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)
	if createReceived != 2 {
		t.Fatalf("incorrect number of create events received after 500 ms (%d vs %d)", createReceived, 2)
	}
	if deleteReceived != 2 {
		t.Fatalf("incorrect number of delete events received after 500 ms (%d vs %d)", deleteReceived, 2)
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}
}

func TestFsnotifyRename(t *testing.T) {
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

	// Add a watch for testDir
	err = watcher.Watch(testDir)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Error {
			t.Fatalf("error received: %s", err)
		}
	}()

	const testFile string = "_test/TestFsnotifyEvents.testfile"
	const testFileRenamed string = "_test/TestFsnotifyEvents.testfileRenamed"

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var renameReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile || event.Name == testFileRenamed {
				if event.IsRename() {
					renameReceived++
				}
				t.Logf("event received: %s", event)
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Create a file
	// This should add at least one event to the fsnotify event queue
	var f *os.File
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	f.WriteString("data")
	f.Sync()
	f.Close()

	// Add a watch for testFile
	err = watcher.Watch(testFile)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	cmd := exec.Command("mv", testFile, testFileRenamed)
	err = cmd.Run()
	if err != nil {
		t.Fatalf("rename failed: %s", err)
	}

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)
	if renameReceived == 0 {
		t.Fatal("fsnotify rename events have not been received after 500 ms")
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(2 * time.Second):
		t.Fatal("event stream was not closed after 2 seconds")
	}

	os.Remove(testFileRenamed)
}

func TestFsnotifyAttrib(t *testing.T) {
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

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Error {
			t.Fatalf("error received: %s", err)
		}
	}()

	const testFile string = "_test/TestFsnotifyAttrib.testfile"

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var attribReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile {
				if event.IsModify() {
					attribReceived++
				}
				t.Logf("event received: %s", event)
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Create a file
	// This should add at least one event to the fsnotify event queue
	var f *os.File
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file failed: %s", err)
	}
	f.Sync()

	f.WriteString("data")
	f.Sync()
	f.Close()

	// Add a watch for testFile
	err = watcher.Watch(testFile)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	cmd := exec.Command("chmod", "0700", testFile)
	err = cmd.Run()
	if err != nil {
		t.Fatalf("chmod failed: %s", err)
	}

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500 * time.Millisecond)
	if attribReceived == 0 {
		t.Fatal("fsnotify attribute events have not received after 500 ms")
	}

	// Try closing the fsnotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(1e9):
		t.Fatal("event stream was not closed after 1 second")
	}

	os.Remove(testFile)
}

func TestFsnotifyClose(t *testing.T) {
	watcher, _ := NewWatcher()
	watcher.Close()

	done := false
	go func() {
		watcher.Close()
		done = true
	}()

	time.Sleep(50e6) // 50 ms
	if !done {
		t.Fatal("double Close() test failed: second Close() call didn't return")
	}

	err := watcher.Watch("_test")
	if err == nil {
		t.Fatal("expected error on Watch() after Close(), got nil")
	}
}
