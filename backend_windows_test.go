//go:build windows

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"strings"
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
		// Count non-empty volume maps (startRead deletes emptied index entries).
		n := 0
		for _, byIndex := range w.b.(*readDirChangesW).watches {
			n += len(byIndex)
		}
		if n != want {
			var d []string
			for k, v := range w.b.(*readDirChangesW).watches {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches (have %d, want %d):\n%v",
				n, want, strings.Join(d, "\n"))
		}
	}

	check(2)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal("expected error adding missing path")
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

// Issue #669: Remove must free watch state even when the path is already gone
// (so getDir/getIno cannot open an inode). Rename keeps the directory handle
// alive while making the original path unresolvable — closer to the leak than
// a full delete, which the I/O thread may already clean via ACCESS_DENIED.
func TestRemoveDeletedPathFreesWatch(t *testing.T) {
	tmp := t.TempDir()
	dir := join(tmp, "dir")
	file := join(dir, "file")
	renamed := join(dir, "file-renamed")
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, file)

	// Rename so the original path no longer exists on disk.
	if err := os.Rename(file, renamed); err != nil {
		t.Fatal(err)
	}
	// Drain rename events so they don't race with Remove's state update.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-w.Events:
		case <-w.Errors:
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Path-based Remove must not panic and should free name entry if present.
	_ = w.Remove(file)

	// Watch directory, wipe it from disk, then Remove by path (issue #669).
	addWatch(t, w, dir)
	rmAll(t, dir)
	if err := w.Remove(dir); err != nil && !errors.Is(err, ErrNonExistentWatch) {
		t.Fatalf("Remove deleted dir: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, byIndex := range w.b.(*readDirChangesW).watches {
		n += len(byIndex)
	}
	if n != 0 {
		t.Fatalf("watches left after Close: %d", n)
	}
}

func TestWindowsRemWatch(t *testing.T) {
	tmp := t.TempDir()

	touch(t, tmp, "file")

	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, tmp)
	if err := w.Remove(tmp); err != nil {
		t.Fatalf("Could not remove the watch: %v", err)
	}
	if err := w.b.(*readDirChangesW).remWatch(tmp); err == nil {
		t.Fatal("Should be fail with closed handle")
	}
}
