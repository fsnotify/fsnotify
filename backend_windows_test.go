//go:build windows

package fsnotify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		got := len(w.WatchList())
		if got != want {
			var d []string
			for k, v := range w.b.(*readDirChangesW).watches {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of watch entries (have %d, want %d):\nwatchlist=%q\ninternal=%v",
				got, want, w.WatchList(), strings.Join(d, "\n"))
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

func TestWindowsRemWatch(t *testing.T) {
	tmp := t.TempDir()

	touch(t, tmp, "file")

	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, tmp)
	if err := w.Remove(tmp); err != nil {
		t.Fatalf("Could not remove the watch: %v\n", err)
	}
	if err := w.b.(*readDirChangesW).remWatch(tmp); err == nil {
		t.Fatal("Should be fail with closed handle\n")
	}
}

func TestWindowsRemWatchRecurseNil(t *testing.T) {
	tmp := t.TempDir()

	w := newWatcher(t)
	defer w.Close()

	// remWatch used to dereference watch.recurse before the nil check, so
	// calling it on an unwatched path with "...\" panicked.
	if err := w.b.(*readDirChangesW).remWatch(tmp + `\...`); err == nil {
		t.Fatal("expected error for non-existent watch")
	}
}

// TestWatchListRace is a regression test for
// https://github.com/fsnotify/fsnotify/issues/709: WatchList() iterating
// watch.names / reading watch.mask raced with the I/O thread mutating
// the same fields from addWatch. Run with -race.
func TestWatchListRace(t *testing.T) {
	root := t.TempDir()
	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, root)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = w.WatchList()
			}
		}
	}()

	for i := 0; i < 50; i++ {
		dir := filepath.Join(root, fmt.Sprintf("d%d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := w.Add(dir); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
}
