//go:build windows

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"strings"
	"testing"
)

func TestRemoveState(t *testing.T) {
	// TODO: the Windows backend is too confusing; needs some serious attention.
	t.Skip("broken test")

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
		if len(w.b.(*readDirChangesW).watches) != want {
			var d []string
			for k, v := range w.b.(*readDirChangesW).watches {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches (have %d, want %d):\n%v",
				len(w.b.(*readDirChangesW).watches), want, strings.Join(d, "\n"))
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
