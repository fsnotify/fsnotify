//go:build freebsd || openbsd || netbsd || dragonfly || darwin

package fsnotify

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDeleteRecreateRace tests a race condition in the kqueue backend (issue #717).
//
// When a file is deleted and quickly recreated with new content, kqueue may
// miss the WRITE event, causing the file to be read before new content is available.
//
// To reliably reproduce the race condition before the fix:
//
//	go test -run TestDeleteRecreateRace -count=100
func TestDeleteRecreateRace(t *testing.T) {
	tmp := t.TempDir()
	dir := join(tmp, "dir")
	mkdirAll(t, dir)
	file := join(dir, "file")

	// Create initial file
	if err := os.WriteFile(file, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := newWatcher(t, dir)

	// Delete and recreate the file quickly
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("recreated"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the recreated content to be visible via events
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case err := <-w.Errors:
			t.Fatal(err)
		case ev := <-w.Events:
			if ev.Name != file {
				continue
			}
			content, err := os.ReadFile(file)
			if err != nil {
				continue // File might not exist on REMOVE
			}
			if string(content) == "recreated" {
				return // Success
			}
		case <-timeout:
			t.Fatal("timeout waiting for recreated content")
		}
	}
}

func TestRemoveState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = join(tmp, "dir")
		file = join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	kq := w.b.(*kqueue)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	check := func(wantUser, wantTotal int) {
		t.Helper()

		if len(kq.watches.path) != wantTotal {
			var d []string
			for k, v := range kq.watches.path {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.path (have %d, want %d):\n%v",
				len(kq.watches.path), wantTotal, strings.Join(d, "\n"))
		}
		if len(kq.watches.wd) != wantTotal {
			var d []string
			for k, v := range kq.watches.wd {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.wd (have %d, want %d):\n%v",
				len(kq.watches.wd), wantTotal, strings.Join(d, "\n"))
		}
		if len(kq.watches.byUser) != wantUser {
			var d []string
			for k, v := range kq.watches.byUser {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.byUser (have %d, want %d):\n%v",
				len(kq.watches.byUser), wantUser, strings.Join(d, "\n"))
		}
	}

	check(2, 3)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal(err)
	}
	check(2, 3)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(1, 2)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check(0, 0)

	// Don't check these after ever remove since they don't map easily to number
	// of files watches. Just make sure they're 0 after everything is removed.
	{
		want := 0
		if len(kq.watches.byDir) != want {
			var d []string
			for k, v := range kq.watches.byDir {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.byDir (have %d, want %d):\n%v",
				len(kq.watches.byDir), want, strings.Join(d, "\n"))
		}

		if len(kq.watches.seen) != want {
			var d []string
			for k, v := range kq.watches.seen {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.seen (have %d, want %d):\n%v",
				len(kq.watches.seen), want, strings.Join(d, "\n"))
			return
		}
	}
}
