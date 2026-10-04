//go:build windows

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// Removing a watch cancels its outstanding ReadDirectoryChangesW, but the
// cancellation completes later, and a read that completed with data just before
// the removal is still queued on the completion port. Those packets must not be
// acted on: the watch's handle is already closed (and may have been reissued),
// and the watch itself may have been garbage collected (#768).
func TestWindowsTeardownWithQueuedCompletions(t *testing.T) {
	t.Run("remove", func(t *testing.T) {
		tmp := t.TempDir()
		dir := join(tmp, "dir")
		mkdir(t, dir)

		w := newWatcher(t)
		errs := collectHandleErrors(w)
		stop := churn(t, dir)

		for i := 0; i < 300; i++ {
			addWatch(t, w, dir)
			if err := w.Remove(dir); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if i%10 == 0 {
				runtime.GC()
			}
		}
		stop()
		closeAndCheck(t, w, errs)
	})

	t.Run("delete watched directory", func(t *testing.T) {
		tmp := t.TempDir()
		w := newWatcher(t)
		errs := collectHandleErrors(w)

		for i := 0; i < 50; i++ {
			dir := join(tmp, fmt.Sprintf("dir%d", i))
			mkdir(t, dir)
			addWatch(t, w, dir)
			stop := churn(t, dir)
			time.Sleep(time.Millisecond)
			stop()
			if err := os.RemoveAll(dir); err != nil {
				t.Fatalf("RemoveAll: %v", err)
			}
			runtime.GC()
		}
		closeAndCheck(t, w, errs)
	})
}

// churn keeps writing files in dir until the returned function is called, so
// completion packets carrying data are queued continuously.
func churn(t *testing.T, dir string) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i%16)), []byte("x"), 0o644)
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

// collectHandleErrors drains w's channels and records the errors that mean a
// torn-down watch was acted on: its closed handle was cancelled or closed again.
func collectHandleErrors(w *Watcher) <-chan []string {
	out := make(chan []string, 1)
	go func() {
		var bad []string
		events, errors := w.Events, w.Errors
		for events != nil || errors != nil {
			select {
			case _, ok := <-events:
				if !ok {
					events = nil
				}
			case err, ok := <-errors:
				if !ok {
					errors = nil
					continue
				}
				msg := err.Error()
				if strings.Contains(msg, "CancelIo") || strings.Contains(msg, "CloseHandle") ||
					strings.Contains(msg, "handle is invalid") {
					bad = append(bad, msg)
				}
			}
		}
		out <- bad
	}()
	return out
}

func closeAndCheck(t *testing.T, w *Watcher, errs <-chan []string) {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- w.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return")
	}
	if bad := <-errs; len(bad) > 0 {
		t.Fatalf("a torn-down watch was acted on (%d errors), first: %s", len(bad), bad[0])
	}
}
