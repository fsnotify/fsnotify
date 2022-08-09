//go:build linux
// +build linux

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Make sure there are no additional threads being created.
//
// TODO: should generalize this and run for all backends.
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
	for i := 0; i < numDirs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			dir := filepath.Join(tmp, strconv.Itoa(i))
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
	file := filepath.Join(tmp, "file")

	touch(t, file)
	fp, err := os.Open(file)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer fp.Close()

	w := newCollector(t, file)
	w.collect(t)

	rm(t, file)
	e := w.events(t)
	cmpEvents(t, tmp, e, newEvents(t, `chmod /file`))

	fp.Close()
	e = w.stop(t)
	cmpEvents(t, tmp, e, newEvents(t, `remove /file`))
}

func TestRemoveState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = filepath.Join(tmp, "dir")
		file = filepath.Join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	check := func(want int) {
		t.Helper()
		if len(w.watches) != want {
			t.Error(w.watches)
		}
		if len(w.paths) != want {
			t.Error(w.paths)
		}
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
}
