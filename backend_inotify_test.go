//go:build linux

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRemoveState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = join(tmp, "dir")
		file = join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	check := func(want int) {
		t.Helper()
		if w.b.(*inotify).watches.len() != want {
			t.Error(w.b.(*inotify).watches)
		}
	}

	check(2)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal(err)
	}
	check(2)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(1)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check(0)

	// Make sure Close() cleans up everything.
	addWatch(t, w, tmp)
	addWatch(t, w, file)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check(0)
}

// Ensure that the correct error is returned on overflows.
func TestInotifyOverflow(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	w := newWatcher(t)
	defer w.Close()

	// We need to generate many more events than the
	// fs.inotify.max_queued_events sysctl setting.
	numDirs, numFiles := 128, 1024

	// All events need to be in the inotify queue before pulling events off it
	// to trigger this error.
	var wg sync.WaitGroup
	for i := range numDirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			dir := join(tmp, strconv.Itoa(i))
			mkdir(t, dir, noWait)
			addWatch(t, w, dir)

			createFiles(t, dir, "", numFiles, 10*time.Second)
		}(i)
	}
	wg.Wait()

	var (
		creates   = 0
		overflows = 0
	)
	for overflows == 0 && creates < numDirs*numFiles {
		select {
		case <-time.After(10 * time.Second):
			t.Fatalf("Not done")
		case err := <-w.Errors:
			if !errors.Is(err, ErrEventOverflow) {
				t.Fatalf("unexpected error from watcher: %v", err)
			}
			overflows++
		case e := <-w.Events:
			if !strings.HasPrefix(e.Name, tmp) {
				t.Fatalf("Event for unknown file: %s", e.Name)
			}
			if e.Op == Create {
				creates++
			}
		}
	}

	if creates == numDirs*numFiles {
		t.Fatalf("could not trigger overflow")
	}
	if overflows == 0 {
		t.Fatalf("no overflow and not enough CREATE events (expected %d, got %d)",
			numDirs*numFiles, creates)
	}
}

// Test inotify's "we don't send REMOVE until all file descriptors are removed"
// behaviour.
func TestInotifyDeleteOpenFile(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	file := join(tmp, "file")

	touch(t, file)
	fp, err := os.Open(file)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer fp.Close()

	w := newCollector(t, file)
	w.collect(t)

	rm(t, file)
	waitForEvents()
	e := w.events(t)
	cmpEvents(t, tmp, e, newEvents(t, `chmod /file`))

	fp.Close()
	e = w.stop(t)
	cmpEvents(t, tmp, e, newEvents(t, `remove /file`))
}

// handleEvent() must not block on w.Errors while it holds w.mu: Add(), Remove()
// and WatchList() need that lock, so a user calling any of them from the
// goroutine that reads w.Errors would never return, and the reader would never
// get its error read.
//
// The only error handleEvent() produces for a non-recursive watch comes from
// inotify_rm_watch() on IN_MOVE_SELF, which fails with EINVAL when the kernel
// already dropped the watch because the moved file was deleted before the
// reader got to the IN_MOVE_SELF.
func TestInotifyErrorNotSentUnderLock(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	file := join(tmp, "file")
	touch(t, file)

	// Always unbuffered: the reader needs to block on sending the Chmod below
	// so that the move and the delete are both queued before it handles them.
	w, err := NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	addWatch(t, w, file)

	chmod(t, 0o600, file, noWait)
	mv(t, file, join(tmp, "moved"), noWait)
	rm(t, join(tmp, "moved"), noWait)

	// The reader is blocked sending this; once read it handles IN_MOVE_SELF
	// for a watch the kernel already removed and reports the EINVAL.
	select {
	case e := <-w.Events:
		if !e.Has(Chmod) {
			t.Fatalf("unexpected first event: %s", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no Chmod event")
	}
	eventSeparator()

	done := make(chan struct{})
	go func() { w.WatchList(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		// Close() needs w.mu too, so read the error to unblock the reader or
		// the deferred Close() hangs as well.
		go func() { <-w.Errors }()
		t.Fatal("WatchList() blocked: handleEvent() is sending on w.Errors while holding w.mu")
	}

	select {
	case err := <-w.Errors:
		if !errors.Is(err, unix.EINVAL) {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no error from inotify_rm_watch()")
	}
}
