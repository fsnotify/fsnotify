// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux

package fsnotify

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var TIMEOUT = 100 * time.Millisecond
var TMP_PREFIX = "_fsnotify_tmp_"

func TestInotifyEvents(t *testing.T) {
	// Create an inotify watcher instance and initialize it
	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher failed: %s", err)
	}

	dir, err := ioutil.TempDir("", TMP_PREFIX)
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

	dir, err := ioutil.TempDir("", TMP_PREFIX)
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
	dir, err := ioutil.TempDir("", TMP_PREFIX)
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

func TestRemoveClose(t *testing.T) {
	dir, err := ioutil.TempDir("", TMP_PREFIX)
	if err != nil {
		t.Fatalf("TempDir failed: %s", err)
	}
	defer os.RemoveAll(dir)

	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher failed: %s", err)
	}
	go func() {
		for err := range watcher.Errors {
			t.Fatalf("error received: %s", err)
		}
	}()
	done := make(chan bool)
	go func() {
		var i int
		for e := range watcher.Events {
			testFileName := filepath.Join(dir, fmt.Sprintf("TestInotifyEvents.%d", i))
			if e.Name != testFileName {
				t.Fatalf("should be %s, but got: %s", testFileName, e.Name)
			}
			// t.Logf("event: %s", e)
			if e.Op&Remove == 0 {
				t.Fatalf("Op should be Remove, but: %s", e.Op)
			}
			i++
			if i == 1000 {
				break
			}
		}
		done <- true
	}()

	for i := 0; i < 1000; i++ {
		testFileName := filepath.Join(dir, fmt.Sprintf("TestInotifyEvents.%d", i))
		testFile, err := os.OpenFile(testFileName, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			t.Fatalf("creating test file: %s", err)
		}
		if err = testFile.Close(); err != nil {
			t.Fatalf("close test file: %s", err)
		}
		if err = watcher.Add(testFileName); err != nil {
			t.Fatalf("Watch(%s) failed: %s", testFileName, err)
		}
		if err = os.Remove(testFileName); err != nil {
			t.Fatalf("remove test file: %s", err)
		}
	}

	// wait for catching up all event
	<-done
	if watcher.length() != 0 {
		t.Fatalf("watcher entries should be 0, but got: %d", watcher.length())
	}

	go func() {
		watcher.Close()
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(TIMEOUT):
		t.Fatal("Close() not returned")
	}

	if watcher.isRunning {
		t.Fatal("still valid after Close()")
	}
}

func TestRmdirClose(t *testing.T) {
	dir, err := ioutil.TempDir("", TMP_PREFIX)
	if err != nil {
		t.Fatalf("TempDir failed: %s", err)
	}
	defer os.RemoveAll(dir)

	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher failed: %s", err)
	}
	go func() {
		for err := range watcher.Errors {
			t.Fatalf("error received: %s", err)
		}
	}()
	done := make(chan bool)
	go func() {
		var i int
		for e := range watcher.Events {
			testName := filepath.Join(dir, fmt.Sprintf("TestInotifyEvents.%d", i))
			if e.Name != testName {
				t.Fatalf("should be %s, but got: %s", testName, e.Name)
			}
			if e.Op&Remove == 0 {
				t.Fatalf("Op should be Remove, but got: %s", e.Op)
			}
			i++
			if i == 1000 {
				break
			}
		}
		done <- true
	}()

	for i := 0; i < 1000; i++ {
		testDir := filepath.Join(dir, fmt.Sprintf("TestInotifyEvents.%d", i))
		err := os.Mkdir(testDir, 0700)
		if err != nil {
			t.Fatalf("creating test dir: %s", err)
		}
		if err = watcher.Add(testDir); err != nil {
			t.Fatalf("Watch(%s) failed: %s", testDir, err)
		}
		if err = os.Remove(testDir); err != nil {
			t.Fatalf("remove test dir: %s", err)
		}
	}

	// wait for all event
	<-done
	if watcher.length() != 0 {
		t.Fatalf("watcher entries should be 0, but got: %d", watcher.length())
	}

	go func() {
		watcher.Close()
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(TIMEOUT):
		t.Fatal("Close() not returned")
	}

	if watcher.isRunning {
		t.Fatal("still valid after Close()")
	}
}

func TestRemoveAll(t *testing.T) {
	dir, err := ioutil.TempDir("", TMP_PREFIX)
	if err != nil {
		t.Fatalf("TempDir failed: %s", err)
	}
	defer os.RemoveAll(dir)

	nevents := make(map[string]int)
	var testDirs [100]string
	for i := 0; i < 100; i++ {
		testDirs[i] = filepath.Join(dir, fmt.Sprintf("rmtest%d", i))
		err = os.Mkdir(testDirs[i], 0700)
		if err != nil {
			t.Fatalf("failed to create testDir: %s", err)
		}
		nevents[testDirs[i]] = 1 // Remove
		for j := 0; j < 10; j++ {
			fname := filepath.Join(testDirs[i], fmt.Sprintf("inotify_testfile%d", j))
			testFile, err := os.OpenFile(fname, os.O_RDWR|os.O_CREATE, 0666)
			if err != nil {
				t.Fatalf("failed to create testfile: %s", err)
			}
			if err := testFile.Close(); err != nil {
				t.Fatalf("failed to close testfile: %s", err)
			}
			nevents[fname] = 1 // Remove
		}
	}

	watcher, err := NewWatcher()
	if err != nil {
		t.Fatalf("could not create TailWatcher: %s", err)
	}
	go func() {
		for err := range watcher.Errors {
			t.Fatalf("error received: %s", err)
		}
	}()
	done := make(chan bool)
	go func() {
		var i int
		for e := range watcher.Events {
			if !strings.HasPrefix(e.Name, dir) {
				t.Fatalf("should have %s, but got: %s", dir, e.Name)
			}
			if e.Op&Remove == 0 {
				t.Fatalf("Op should be Remove, but got: %s", e)
			}
			nevents[e.Name] = nevents[e.Name] - 1
			if nevents[e.Name] < 0 {
				t.Fatalf("receive unexpected event: %s", e)
			}
			i++
			if i == 1100 {
				// 100 dirs, each have 10 files
				break
			}
		}
		done <- true
	}()

	for i := 0; i < 100; i++ {
		if err = watcher.Add(testDirs[i]); err != nil {
			t.Fatalf("failed to Add to Watcher: %s", err)
		}
	}
	for i := 0; i < 100; i++ {
		if err = os.RemoveAll(testDirs[i]); err != nil {
			t.Fatalf("failed to RemoveAll: %s", err)
		}
	}

	<-done
	time.Sleep(2 * time.Second)
	if watcher.length() != 0 {
		for k, _ := range watcher.watches {
			t.Logf("still watching path: %s", k)
		}
		t.Fatalf("watcher entries should be 0, but got: %d", watcher.length())
	}

	watcher.Close()
	if watcher.isRunning {
		t.Fatal("still valid after Close()")
	}
}
