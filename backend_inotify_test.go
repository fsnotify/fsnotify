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
	"syscall"
	"testing"
	"time"
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

// Removing a watch must not panic when the watched directory was atomically
// replaced with another watched directory via rename(2) and Add() was called
// for the replaced path before the reader goroutine processed the queued
// IN_DELETE_SELF/IN_IGNORED for the old watch descriptor. In that case the
// path entry in w.watches still points at the dead descriptor and w.wd[wd]
// is nil. See #798.
func TestInotifyRemoveAfterRenameReplace(t *testing.T) {
	t.Parallel()

	var (
		tmp = t.TempDir()
		x   = join(tmp, "x")
		y   = join(tmp, "y")
	)
	mkdir(t, x)
	mkdir(t, y)

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, tmp)
	addWatch(t, w, x)
	addWatch(t, w, y)

	// Atomically replace x with y; os.Rename refuses directory targets, so
	// use the raw syscall.
	if err := syscall.Rename(y, x); err != nil {
		t.Fatal(err)
	}

	// Don't drain w.Events: the reader goroutine blocks on sending the
	// queued IN_DELETE_SELF before it reaches IN_IGNORED, which makes the
	// interleaving below deterministic.
	time.Sleep(100 * time.Millisecond)

	// inotify_add_watch() returns y's already-tracked descriptor for the new
	// x, leaving the w.watches entry for x pointing at the dead descriptor.
	addWatch(t, w, x)

	// Must not panic with a nil pointer dereference; there is no kernel
	// watch left to remove, so this reports success.
	if err := w.Remove(x); err != nil {
		t.Fatalf("Remove(%q): %s", x, err)
	}

	// The stale entry must be gone; tmp and y are still watched.
	if n := w.b.(*inotify).watches.len(); n != 2 {
		t.Fatalf("have %d watches; want 2", n)
	}
	for _, p := range w.WatchList() {
		if p == x {
			t.Fatalf("%q still in watch list", x)
		}
	}
}
