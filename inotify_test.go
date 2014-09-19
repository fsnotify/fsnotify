// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux

package fsnotify

import (
	"io/ioutil"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

var TIMEOUT = 100 * time.Millisecond

func TestInotifyEvents(t *testing.T) {
	// Create an inotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher failed: %s", err)
	}

	dir, err := ioutil.TempDir("", "inotify")
	if err != nil {
		t.Fatalf("TempDir failed: %s", err)
	}
	defer os.RemoveAll(dir)

	// Add a watch for "_test"
	err = watcher.Add(dir)
	if err != nil {
		t.Fatalf("Watch failed: %s", err)
	}

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Errors {
			t.Fatalf("error received: %s", err)
		}
	}()

	testFile := dir + "/TestInotifyEvents.testfile"

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Events
	var eventsReceived int32 = 0
	done := make(chan bool)
	go func() {
		for event := range eventstream {
			// Only count relevant events
			if event.Name == testFile {
				atomic.AddInt32(&eventsReceived, 1)
				t.Logf("event received: %s", event)
			} else {
				t.Logf("unexpected event received: %s", event)
			}
		}
		done <- true
	}()

	// Create a file
	// This should add at least one event to the inotify event queue
	_, err = os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file: %s", err)
	}

	// We expect this event to be received almost immediately, but let's wait 10ms to be sure
	time.Sleep(TIMEOUT)
	if atomic.AddInt32(&eventsReceived, 0) == 0 {
		t.Fatal("inotify event hasn't been received after 100ms")
	}

	// Try closing the inotify instance
	t.Log("calling Close()")
	watcher.Close()
	t.Log("waiting for the event channel to become closed...")
	select {
	case <-done:
		t.Log("event channel closed")
	case <-time.After(TIMEOUT):
		t.Fatal("event stream was not closed after 100ms")
	}
}

func TestInotifyClose(t *testing.T) {
	watcher, _ := NewWatcher()
	if err := watcher.Close(); err != nil {
		t.Fatalf("close returns: %s", err)
	}
	if watcher.isRunning {
		t.Fatal("still valid after Close()")
	}

	done := make(chan bool)
	go func() {
		if err := watcher.Close(); err != nil {
			t.Logf("second close returns: %s", err)
		}
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(TIMEOUT):
		t.Fatal("double Close() test failed: second Close() call didn't return")
	}

	err := watcher.Add(os.TempDir())
	if err == nil {
		t.Fatal("expected error on Watch() after Close(), got nil")
	}
}

func TestIgnoredEvents(t *testing.T) {
	// Create an inotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher failed: %s", err)
	}

	dir, err := ioutil.TempDir("", "inotify")
	if err != nil {
		t.Fatalf("TempDir failed: %s", err)
	}
	defer os.RemoveAll(dir)

	// Add a watch for "_test"
	err = watcher.Add(dir)
	if err != nil {
		t.Fatalf("Watch failed: %s", err)
	}

	// Receive errors on the error channel on a separate goroutine
	go func() {
		for err := range watcher.Errors {
			t.Fatalf("error received: %s", err)
		}
	}()

	testFileName := dir + "/TestInotifyEvents.testfile"

	// Receive events on the event channel on a separate goroutine
	eventstream := watcher.Events
	var event Event

	// IN_CREATE
	testFile, err := os.OpenFile(testFileName, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("creating test file: %s", err)
	}
	event = <-eventstream
	if event.Op&Create == 0 {
		t.Fatal("inotify hasn't received IN_CREATE")
	}
	if err = testFile.Close(); err != nil {
		t.Fatalf("close: %s", err)
	}

	// IN_DELETE
	if err = os.Remove(testFileName); err != nil {
		t.Fatal("removing test file: %s", err)
	}
	event = <-eventstream
	if event.Op&Remove == 0 {
		t.Fatal("inotify hasn't received IN_DELETE")
	}

	// IN_DELETE_SELF, IN_IGNORED
	os.RemoveAll(dir)
	event = <-eventstream
	if event.Op&Remove == 0 {
		t.Fatal("inotify hasn't received IN_DELETE_SELF")
	}

	// mk/rm dir repeatedly
	for j := 0; j < 64; j++ {
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll tempdir[%s] again failed: %s", dir, err)
		}
		if err = watcher.Add(dir); err != nil {
			t.Fatalf("Watch failed: %s", err)
		}
		os.RemoveAll(dir)
		// IN_DELETE_SELF, IN_IGNORED
		event = <-eventstream
		if event.Op&Remove == 0 {
			t.Fatal("inotify hasn't received IN_DELETE_SELF")
		}
		if event.Name != dir {
			t.Fatalf("received different name event: %s", event.Name)
		}
	}

	// wait for catching up to inotify IGNORE event
	time.Sleep(TIMEOUT)
	if watcher.length() != 0 {
		t.Fatalf("watcher entries should be 0, but got: %d", watcher.length())
	}
	watcher.Close()
	if watcher.isRunning {
		t.Fatal("still running after Close()")
	}
}

func TestRemoveWatch(t *testing.T) {
	// Create an inotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher failed: %s", err)
	}
	dir, err := ioutil.TempDir("", "inotify")
	if err != nil {
		t.Fatalf("TempDir failed: %s", err)
	}
	defer os.RemoveAll(dir)

	if err = watcher.Add(dir); err != nil {
		t.Fatalf("Watch failed: %s", err)
	}

	if err = watcher.Remove(dir); err != nil {
		t.Fatalf("Remove failed: %s, err")
	}

	if watcher.length() != 0 {
		t.Fatalf("watcher entries should be 0, but got: %d", watcher.length())
	}
	watcher.Close()
	if watcher.isRunning {
		t.Fatal("still running after Close()")
	}
}
