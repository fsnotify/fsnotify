//go:build windows
// +build windows

package fsnotify

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveState(t *testing.T) {
	// TODO: the Windows backend is too confusing; needs some serious attention.
	return

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
			var d []string
			for k, v := range w.watches {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches (have %d, want %d):\n%v",
				len(w.watches), want, strings.Join(d, "\n"))
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

func TestWindowsNoAttributeChanges(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "TestFsnotifyEventsExisting.testfile")

	touch(t, file) // Create a file before watching directory
	w := newCollector(t, tmp)
	w.collect(t)
	chmod(t, 0o400, file) // Make the file read-only, which is an attribute change

	have := w.stop(t)
	if len(have) != 0 {
		t.Fatalf("should not have received any events, received:\n%s", have)
	}
}

// TODO: write test which makes sure the buffer size is set correctly.
func TestWindowsWithBufferSize(t *testing.T) {
	getWatch := func(opts ...addOpt) (*watch, error) {
		w, err := NewWatcher()
		if err != nil {
			return nil, err
		}
		if err := w.AddWith(t.TempDir(), opts...); err != nil {
			return nil, err
		}

		// Hackery to get first (and only) map value.
		var v indexMap
		for _, v = range w.watches {
		}
		if len(v) != 1 {
			t.Fatal()
		}
		var watch *watch
		for _, watch = range v {
		}
		return watch, nil
	}

	check := func(w *watch, want int) {
		if len(w.buf) != want || cap(w.buf) != want {
			t.Fatalf("want = %d; len = %d; cap = %d", want, len(w.buf), cap(w.buf))
		}
	}

	if w, err := getWatch(); err != nil {
		t.Fatal(err)
	} else {
		check(w, 65536)
	}
	if w, err := getWatch(WithBufferSize(4096)); err != nil {
		t.Fatal(err)
	} else {
		check(w, 4096)
	}

	if _, err := getWatch(WithBufferSize(1024)); err == nil || !strings.Contains(err.Error(), "cannot be smaller") {
		t.Fatal(err)
	}
}
