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
