//go:build windows
// +build windows

package fsnotify

import (
	"path/filepath"
	"testing"
)

func TestWindowsRemWatch(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")

	touch(t, file)

	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, tmp)
	if err := w.Remove(tmp); err != nil {
		t.Fatalf("Could not remove the watch: %v\n", err)
	}
	if err := w.remWatch(tmp); err == nil {
		t.Fatal("Should be fail with closed handle\n")
	}
}
