//go:build linux
// +build linux

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TODO: I'm not sure if these tests are still needed; I think they've become
//       redundant after epoll was removed. Keep them for now to be sure.
func TestInotifyClose(t *testing.T) {
	isWatcherReallyClosed := func(t *testing.T, w *Watcher) {
		select {
		case err, ok := <-w.Errors:
			if ok {
				t.Fatalf("w.Errors is not closed; readEvents is still alive after closing (error: %v)", err)
			}
		default:
			t.Fatalf("w.Errors would have blocked; readEvents is still alive!")
		}
		select {
		case _, ok := <-w.Events:
			if ok {
				t.Fatalf("w.Events is not closed; readEvents is still alive after closing")
			}
		default:
			t.Fatalf("w.Events would have blocked; readEvents is still alive!")
		}
	}

	t.Run("close immediately", func(t *testing.T) {
		t.Parallel()
		w := newWatcher(t)

		w.Close()                           // Close immediately; it won't even reach the first unix.Read.
		<-time.After(50 * time.Millisecond) // Wait for the close to complete.
		isWatcherReallyClosed(t, w)
	})

	t.Run("close slightly later", func(t *testing.T) {
		t.Parallel()
		w := newWatcher(t)

		<-time.After(50 * time.Millisecond) // Wait until readEvents has reached unix.Read, and Close.
		w.Close()
		<-time.After(50 * time.Millisecond) // Wait for the close to complete.
		isWatcherReallyClosed(t, w)
	})

	t.Run("close slightly later with watch", func(t *testing.T) {
		t.Parallel()
		w := newWatcher(t)
		addWatch(t, w, t.TempDir())

		<-time.After(50 * time.Millisecond) // Wait until readEvents has reached unix.Read, and Close.
		w.Close()
		<-time.After(50 * time.Millisecond) // Wait for the close to complete.
		isWatcherReallyClosed(t, w)
	})

	t.Run("close after read", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		w := newWatcher(t)
		addWatch(t, w, tmp)

		touch(t, tmp, "somethingSOMETHINGsomethingSOMETHING") // Generate an event.

		<-time.After(50 * time.Millisecond) // Wait for readEvents to read the event, then close the watcher.
		w.Close()
		<-time.After(50 * time.Millisecond) // Wait for the close to complete.
		isWatcherReallyClosed(t, w)
	})

	t.Run("replace after close", func(t *testing.T) {
		t.Parallel()

		tmp := t.TempDir()
		w := newWatcher(t)
		defer w.Close()

		addWatch(t, w, tmp)
		touch(t, tmp, "testfile")
		select {
		case <-w.Events:
		case err := <-w.Errors:
			t.Fatalf("Error from watcher: %v", err)
		case <-time.After(50 * time.Millisecond):
			t.Fatalf("Took too long to wait for event")
		}

		// At this point, we've received one event, so the goroutine is ready and it
		// blocking on unix.Read. Now try to swap the file descriptor under its
		// nose.
		w.Close()
		w, err := NewWatcher()
		defer func() { _ = w.Close() }()
		if err != nil {
			t.Fatalf("Failed to create second watcher: %v", err)
		}

		<-time.After(50 * time.Millisecond)
		err = w.Add(tmp)
		if err != nil {
			t.Fatalf("Error adding tmp dir again: %v", err)
		}
	})
}

// Verify the watcher can keep up with file creations/deletions when under load.
//
// TODO: should probably be in integrations_test.
func TestInotifyStress(t *testing.T) {
	maxNumToCreate := 1000

	tmp := t.TempDir()
	testFilePrefix := filepath.Join(tmp, "testfile")

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, tmp)

	doneChan := make(chan struct{})
	// The buffer ensures that the file generation goroutine is never blocked.
	errChan := make(chan error, 2*maxNumToCreate)

	go func() {
		for i := 0; i < maxNumToCreate; i++ {
			testFile := fmt.Sprintf("%s%d", testFilePrefix, i)

			handle, err := os.Create(testFile)
			if err != nil {
				errChan <- fmt.Errorf("Create failed: %v", err)
				continue
			}

			err = handle.Close()
			if err != nil {
				errChan <- fmt.Errorf("Close failed: %v", err)
				continue
			}
		}

		// If we delete a newly created file too quickly, inotify will skip the
		// create event and only send the delete event.
		time.Sleep(100 * time.Millisecond)

		for i := 0; i < maxNumToCreate; i++ {
			testFile := fmt.Sprintf("%s%d", testFilePrefix, i)
			err := os.Remove(testFile)
			if err != nil {
				errChan <- fmt.Errorf("Remove failed: %v", err)
			}
		}

		close(doneChan)
	}()

	creates := 0
	removes := 0

	finished := false
	after := time.After(10 * time.Second)
	for !finished {
		select {
		case <-after:
			t.Fatalf("Not done")
		case <-doneChan:
			finished = true
		case err := <-errChan:
			t.Fatalf("Got an error from file creator goroutine: %v", err)
		case err := <-w.Errors:
			t.Fatalf("Got an error from watcher: %v", err)
		case evt := <-w.Events:
			if !strings.HasPrefix(evt.Name, testFilePrefix) {
				t.Fatalf("Got an event for an unknown file: %s", evt.Name)
			}
			if evt.Op == Create {
				creates++
			}
			if evt.Op == Remove {
				removes++
			}
		}
	}

	// Drain remaining events from channels
	count := 0
	for count < 10 {
		select {
		case err := <-errChan:
			t.Fatalf("Got an error from file creator goroutine: %v", err)
		case err := <-w.Errors:
			t.Fatalf("Got an error from watcher: %v", err)
		case evt := <-w.Events:
			if !strings.HasPrefix(evt.Name, testFilePrefix) {
				t.Fatalf("Got an event for an unknown file: %s", evt.Name)
			}
			if evt.Op == Create {
				creates++
			}
			if evt.Op == Remove {
				removes++
			}
			count = 0
		default:
			count++
			// Give the watcher chances to fill the channels.
			time.Sleep(time.Millisecond)
		}
	}

	if creates-removes > 1 || creates-removes < -1 {
		t.Fatalf("Creates and removes should not be off by more than one: %d creates, %d removes", creates, removes)
	}
	if creates < 50 {
		t.Fatalf("Expected at least 50 creates, got %d", creates)
	}
}

func TestInotifyInnerMapLength(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	file := filepath.Join(tmp, "testfile")

	touch(t, file)

	w := newWatcher(t)
	addWatch(t, w, file)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for err := range w.Errors {
			t.Errorf("error received: %s", err)
		}
	}()

	rm(t, file)
	<-w.Events                          // consume Remove event
	<-time.After(50 * time.Millisecond) // wait IN_IGNORE propagated

	func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.watches) != 0 {
			t.Fatalf("Expected watches len is 0, but got: %d, %v", len(w.watches), w.watches)
		}
		if len(w.paths) != 0 {
			t.Fatalf("Expected paths len is 0, but got: %d, %v", len(w.paths), w.paths)
		}
	}()

	w.Close()
	wg.Wait()
}

func TestInotifyOverflow(t *testing.T) {
	t.Parallel()

	// We need to generate many more events than the
	// fs.inotify.max_queued_events sysctl setting. We use multiple goroutines
	// (one per directory) to speed up file creation.
	numDirs := 128
	numFiles := 1024

	tmp := t.TempDir()
	w := newWatcher(t)
	defer w.Close()

	for dn := 0; dn < numDirs; dn++ {
		dir := fmt.Sprintf("%s/%d", tmp, dn)
		mkdir(t, dir, noWait)
		addWatch(t, w, dir)
	}

	errChan := make(chan error, numDirs*numFiles)

	// All events need to be in the inotify queue before pulling events off it
	// to trigger this error.
	wg := sync.WaitGroup{}
	for dn := 0; dn < numDirs; dn++ {
		dir := fmt.Sprintf("%s/%d", tmp, dn)

		wg.Add(1)
		go func() {
			for fn := 0; fn < numFiles; fn++ {
				testFile := fmt.Sprintf("%s/%d", dir, fn)

				handle, err := os.Create(testFile)
				if err != nil {
					errChan <- fmt.Errorf("Create failed: %v", err)
					continue
				}

				err = handle.Close()
				if err != nil {
					errChan <- fmt.Errorf("Close failed: %v", err)
					continue
				}
			}
			wg.Done()
		}()
	}
	wg.Wait()

	creates := 0
	overflows := 0

	after := time.After(10 * time.Second)
	for overflows == 0 && creates < numDirs*numFiles {
		select {
		case <-after:
			t.Fatalf("Not done")
		case err := <-errChan:
			t.Fatalf("Got an error from file creator goroutine: %v", err)
		case err := <-w.Errors:
			if err == ErrEventOverflow {
				overflows++
			} else {
				t.Fatalf("Got an error from watcher: %v", err)
			}
		case evt := <-w.Events:
			if !strings.HasPrefix(evt.Name, tmp) {
				t.Fatalf("Got an event for an unknown file: %s", evt.Name)
			}
			if evt.Op == Create {
				creates++
			}
		}
	}

	if creates == numDirs*numFiles {
		t.Fatalf("Could not trigger overflow")
	}

	if overflows == 0 {
		t.Fatalf("No overflow and not enough creates (expected %d, got %d)",
			numDirs*numFiles, creates)
	}
}

func TestInotifyNoBlockingSyscalls(t *testing.T) {
	test := func() error {
		getThreads := func() (int, error) {
			// return pprof.Lookup("threadcreate").Count()
			d := fmt.Sprintf("/proc/%d/task", os.Getpid())
			ls, err := os.ReadDir(d)
			if err != nil {
				return 0, fmt.Errorf("reading %q: %s", d, err)
			}
			return len(ls), nil
		}

		w := newWatcher(t)
		start, err := getThreads()
		if err != nil {
			return err
		}

		// Call readEvents a bunch of times; if this function has a blocking raw
		// syscall, it'll create many new kthreads
		for i := 0; i <= 60; i++ {
			go w.readEvents()
		}

		time.Sleep(2 * time.Second)

		end, err := getThreads()
		if err != nil {
			return err
		}
		if diff := end - start; diff > 0 {
			return fmt.Errorf("Got a nonzero diff %v. starting: %v. ending: %v", diff, start, end)
		}
		return nil
	}

	// This test can be a bit flaky, so run it twice and consider it "failed"
	// only if both fail.
	err := test()
	if err != nil {
		time.Sleep(2 * time.Second)
		err := test()
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TODO: the below should probably be in integration_test, as they're not really
//       inotify-specific as far as I can see.

func TestInotifyRemoveTwice(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "testfile")

	touch(t, testFile)

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, testFile)

	err := w.Remove(testFile)
	if err != nil {
		t.Fatal(err)
	}

	err = w.Remove(testFile)
	if err == nil {
		t.Fatalf("no error on removing invalid file")
	} else if !errors.Is(err, ErrNonExistentWatch) {
		t.Fatalf("remove %q: %s", testFile, err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.watches) != 0 {
		t.Fatalf("Expected watches len is 0, but got: %d, %v", len(w.watches), w.watches)
	}
	if len(w.paths) != 0 {
		t.Fatalf("Expected paths len is 0, but got: %d, %v", len(w.paths), w.paths)
	}
}

func TestInotifyWatchList(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "testfile")

	touch(t, testFile)

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, testFile)
	addWatch(t, w, tmp)

	value := w.WatchList()

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, entry := range value {
		if _, ok := w.watches[entry]; !ok {
			t.Fatal("return value of WatchList is not same as the expected")
		}
	}
}

func TestInotifyDeleteOpenedFile(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "testfile")

	fd, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer fd.Close()

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, testFile)

	checkEvent := func(exp Op) {
		select {
		case event := <-w.Events:
			t.Logf("Event received: %s", event.Op)
			if event.Op != exp {
				t.Fatalf("Event expected: %s, got: %s", exp, event.Op)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Expected %s event not received", exp)
		}
	}

	// Remove the (opened) file, check Chmod event (notifying about file link
	// count change) is received
	rm(t, testFile)
	checkEvent(Chmod)

	// Close the file, check Remove event is received
	fd.Close()
	checkEvent(Remove)
}
