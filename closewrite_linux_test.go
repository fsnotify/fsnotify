//go:build linux

package fsnotify_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestCloseWriteAfterChildWriterCloses(t *testing.T) {
	t.Parallel()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	dir := t.TempDir()
	if !w.Supports(fsnotify.UnportableCloseWrite) {
		t.Fatal("close-write support unavailable on Linux")
	}
	if err := w.AddWith(dir, fsnotify.WithOps(fsnotify.UnportableCloseWrite)); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "file")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("content"); err != nil {
		file.Close()
		t.Fatal(err)
	}

	select {
	case event := <-w.Events:
		file.Close()
		t.Fatalf("event arrived before writer closed: %s", event)
	case err := <-w.Errors:
		file.Close()
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-w.Events:
		if event.Name != path || !event.Has(fsnotify.UnportableCloseWrite) {
			t.Fatalf("event = %s, want CLOSE_WRITE for %q", event, path)
		}
	case err := <-w.Errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for close-write event")
	}
}
