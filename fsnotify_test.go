// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fsnotify

import (
	"os"
	"time"
	"testing"
	"exec"
)

func TestFsnotifyEvents(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"

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

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var eventsReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile {
				eventsReceived++
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

	// Add a watch for testFile
	err = watcher.Watch(testFile)
	if err != nil {
		t.Fatalf("Watcher.Watch() failed: %s", err)
	}

	f.WriteString("data")
	f.Sync()
	f.Close()

	os.Remove(testFile)

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500e6) // 500 ms
	if eventsReceived == 0 {
		t.Fatal("fsnotify event hasn't been received after 500 ms")
	}

	t.Logf("Received %d events.", eventsReceived)

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
}

func TestFsnotifyDirOnly(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"

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

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var eventsReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile {
				eventsReceived++
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

	os.Remove(testFile)

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500e6) // 500 ms
	if eventsReceived == 0 {
		t.Fatal("fsnotify event hasn't been received after 500 ms")
	}

	t.Logf("Received %d events.", eventsReceived)

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
}

func TestFsnotifyRename(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"

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
	var eventsReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile || event.Name == testFileRenamed {
				eventsReceived++
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

	os.Remove(testFileRenamed)

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500e6) // 500 ms
	if eventsReceived == 0 {
		t.Fatal("fsnotify event hasn't been received after 500 ms")
	}

	t.Logf("Received %d events.", eventsReceived)

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
}

func TestFsnotifyAttrib(t *testing.T) {
	// Create an fsnotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher() failed: %s", err)
	}

	const testDir string = "_test"

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

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Event
	var eventsReceived = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testDir || event.Name == testFile {
				eventsReceived++
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

	os.Remove(testFile)

	// We expect this event to be received almost immediately, but let's wait 500 ms to be sure
	time.Sleep(500e6) // 500 ms
	if eventsReceived == 0 {
		t.Fatal("fsnotify event hasn't been received after 500 ms")
	}

	t.Logf("Received %d events.", eventsReceived)

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
