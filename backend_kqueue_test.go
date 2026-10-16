//go:build freebsd || openbsd || netbsd || dragonfly || darwin

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestKqueueRenameDirectory(t *testing.T) {
	for _, tt := range []struct {
		name       string
		createFile bool
		writeFile  bool
		watchFile  bool
	}{
		{name: "directory"},
		{name: "pending directory write", createFile: true},
		{name: "pending file write", writeFile: true},
		{name: "explicit file watch", watchFile: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			dir, renamed := join(tmp, "dir"), join(tmp, "renamed")
			file := join(dir, "file")
			mkdir(t, dir, noWait)
			touch(t, file, noWait)

			// Start reading only after the file operations, so changes to a
			// directory are coalesced even on a fast machine.
			fd, pipe, err := newKqueue()
			if err != nil {
				t.Fatal(err)
			}
			events, errs := make(chan Event, testBuffered), make(chan error)
			w := &kqueue{
				shared: newShared(events, errs), Events: events, Errors: errs,
				kq: fd, closepipe: pipe, watches: newWatches(),
			}
			started := false
			t.Cleanup(func() {
				if !started {
					go w.readEvents()
				}
				if err := w.Close(); err != nil {
					t.Error(err)
				}
				for range events {
				}
			})
			if err := w.Add(dir); err != nil {
				t.Fatal(err)
			}
			if tt.watchFile {
				if err := w.Add(file); err != nil {
					t.Fatal(err)
				}
			}
			child, ok := w.watches.byPath(file)
			if !ok {
				t.Fatal("missing file watch")
			}
			if tt.createFile {
				touch(t, dir, "new", noWait)
			}
			mv(t, dir, renamed, noWait)
			if tt.writeFile {
				echoAppend(t, "data", renamed, "file", noWait)
			}
			go w.readEvents()
			started = true

			select {
			case e := <-events:
				if e.Name != dir || !e.Has(Rename) {
					t.Fatalf("have %s; want Rename %q", e, dir)
				}
			case err := <-errs:
				t.Fatal(err)
			case <-time.After(2 * time.Second):
				t.Fatal("no Rename event")
			}

			var want []string
			if tt.watchFile {
				want = []string{file}
			}
			if have := w.watches.listPaths(false); !slices.Equal(have, want) {
				t.Errorf("watches after rename: have %q; want %q", have, want)
			}
			var st unix.Stat_t
			err = unix.Fstat(child.wd, &st)
			if tt.watchFile {
				if err != nil {
					t.Fatalf("explicit file watch was closed: %s", err)
				}
				echoAppend(t, "data", renamed, "file", noWait)
				select {
				case e := <-events:
					if e.Name != file || !e.Has(Write) {
						t.Fatalf("have %s; want Write %q", e, file)
					}
				case err := <-errs:
					t.Fatal(err)
				case <-time.After(2 * time.Second):
					t.Fatal("no Write event for explicit file watch")
				}
			} else if !errors.Is(err, unix.EBADF) {
				t.Errorf("file descriptor after rename: have %v; want EBADF", err)
			}

			// A child event already returned by kevent must not be emitted
			// with an empty name after its internal watch has been removed.
			select {
			case e := <-events:
				t.Errorf("unexpected event after rename: %s", e)
			case err := <-errs:
				t.Error(err)
			case <-time.After(50 * time.Millisecond):
			}
		})
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

	// Make sure Close() cleans up everything.
	addWatch(t, w, tmp)
	addWatch(t, w, file)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
}
