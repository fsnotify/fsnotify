//go:build freebsd || openbsd || netbsd || dragonfly || darwin

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestConcurrentAdd(t *testing.T) {
	dir := t.TempDir()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t)
	defer w.Close()
	kq := w.b.(*kqueue)

	// Directory entries are registered by the reader at the same time that a
	// user may Add them in response to a Create event.
	const count = 64
	start := make(chan struct{})
	errs := make(chan error, count)
	var done sync.WaitGroup
	for i := range count {
		done.Add(1)
		go func() {
			defer done.Done()
			<-start
			if i%2 == 0 {
				errs <- w.Add(dir)
			} else {
				_, err := kq.internalWatch(dir, fi)
				errs <- err
			}
		}()
	}
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	kq.watches.mu.RLock()
	if len(kq.watches.wd) != 1 {
		t.Errorf("registered %d descriptors for one directory, want 1", len(kq.watches.wd))
	}
	kq.watches.mu.RUnlock()
	info, ok := kq.watches.byPath(dir)
	if !ok || info.dirFlags != noteAllEvents {
		t.Errorf("directory flags: %#x, want %#x", info.dirFlags, noteAllEvents)
	}

	if err := w.Remove(dir); err != nil {
		t.Fatal(err)
	}
	kq.watches.mu.RLock()
	defer kq.watches.mu.RUnlock()
	if len(kq.watches.wd) != 0 {
		t.Errorf("Remove left %d descriptors registered", len(kq.watches.wd))
	}
}

func TestInternalWatchPreservesDirectoryEvents(t *testing.T) {
	dir := t.TempDir()
	w := newCollector(t, dir)
	w.collect(t)
	kq := w.w.b.(*kqueue)

	// Emulate an internal registration with flags read before the user Add.
	// EV_ADD replaces the kernel flags, so these must be merged with the
	// current flags before registering the existing descriptor again.
	if _, err := kq.addWatch(dir, unix.NOTE_DELETE|unix.NOTE_RENAME, true); err != nil {
		t.Fatal(err)
	}
	info, _ := kq.watches.byPath(dir)
	if info.dirFlags != noteAllEvents {
		t.Errorf("internal registration replaced directory flags: %#x, want %#x", info.dirFlags, noteAllEvents)
	}

	touch(t, dir, "file")
	cmpEvents(t, dir, w.stop(t), newEvents(t, `
		create  /file
	`))
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
