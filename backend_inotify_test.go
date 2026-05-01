//go:build linux

package fsnotify

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
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
}

func TestRemoveRecursiveDoesNotRemoveSiblingPrefixWatch(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mkdir(t, tmp, "a")
	mkdir(t, tmp, "ab")

	w := newWatcher(t)
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	})

	addWatch(t, w, tmp, "a", "...")
	addWatch(t, w, tmp, "ab", "...")

	have := w.WatchList()
	slices.Sort(have)
	want := []string{join(tmp, "a"), join(tmp, "ab")}
	if !slices.Equal(have, want) {
		t.Fatalf("before remove: watch list = %#v, want %#v", have, want)
	}

	if err := w.Remove(join(tmp, "a", "...")); err != nil {
		t.Fatal(err)
	}

	have = w.WatchList()
	if len(have) != 1 || have[0] != join(tmp, "ab") {
		t.Fatalf("after remove: watch list = %#v, want %#v", have, []string{join(tmp, "ab")})
	}
}

func TestRenameRecursiveDoesNotRenameSiblingPrefixWatch(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mkdir(t, tmp, "a")
	mkdir(t, tmp, "a", "sub")
	mkdir(t, tmp, "ab")
	mkdir(t, tmp, "ab", "sub")

	w := newWatcher(t)
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	})
	go func() {
		for range w.Events {
		}
	}()
	go func() {
		for range w.Errors {
		}
	}()

	addWatch(t, w, tmp, "...")

	have := w.WatchList()
	slices.Sort(have)
	want := []string{tmp, join(tmp, "a"), join(tmp, "a", "sub"), join(tmp, "ab"), join(tmp, "ab", "sub")}
	if !slices.Equal(have, want) {
		t.Fatalf("before rename: watch list = %#v, want %#v", have, want)
	}

	mv(t, join(tmp, "a"), tmp, "x")

	// rename rewrites watch paths in the wd map directly (WatchList reads
	// the path map, which the rewrite does not touch), so inspect the wd
	// map paths to observe whether siblings were incorrectly renamed.
	wdPaths := func() []string {
		inot := w.b.(*inotify)
		inot.mu.Lock()
		defer inot.mu.Unlock()
		paths := make([]string, 0, len(inot.watches.wd))
		for _, ww := range inot.watches.wd {
			paths = append(paths, ww.path)
		}
		slices.Sort(paths)
		return paths
	}

	wantAfter := []string{tmp, join(tmp, "ab"), join(tmp, "ab", "sub"), join(tmp, "x"), join(tmp, "x", "sub")}
	deadline := time.Now().Add(2 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = wdPaths()
		if slices.Equal(got, wantAfter) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("after rename: wd paths = %#v, want %#v", got, wantAfter)
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
