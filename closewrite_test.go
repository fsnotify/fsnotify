package fsnotify_test

import (
	"runtime"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestCloseWriteCapabilityMatchesBackend(t *testing.T) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	want := runtime.GOOS == "linux" || runtime.GOOS == "android"
	if got := w.Supports(fsnotify.UnportableCloseWrite); got != want {
		t.Fatalf("Supports(UnportableCloseWrite) = %t, want %t on %s", got, want, runtime.GOOS)
	}
}
