//go:build freebsd || openbsd || netbsd || dragonfly || darwin

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

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

// TestCloseWhileWatchingNewFiles is a regression test for Close racing with
// readEvents: Close released a snapshot of the watches while readEvents kept
// watching the files created in the directory, so the ones it added after the
// snapshot leaked and a descriptor could be closed twice, the second time
// after its number was reused by an unrelated file.
func TestCloseWhileWatchingNewFiles(t *testing.T) {
	tmp := t.TempDir()
	for i := range 20 {
		touch(t, join(tmp, fmt.Sprint(i)))
	}

	stop := make(chan struct{})
	churned := make(chan struct{})
	go func() {
		defer close(churned)
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			p := join(tmp, fmt.Sprintf("new%d", n%10))
			_ = os.WriteFile(p, nil, 0o644)
			_ = os.Remove(p)
		}
	}()

	openFDs := func() int {
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := openFDs()
	for range 50 {
		w, err := NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for range w.Errors {
			}
		}()
		go func() {
			for range w.Events {
			}
		}()
		// The directory may list a file the churn removes before it is watched.
		for w.Add(tmp) != nil {
		}
		time.Sleep(2 * time.Millisecond)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-churned

	if leaked := openFDs() - before; leaked > 0 {
		t.Fatalf("%d descriptors leaked by 50 watchers closed while watching new files", leaked)
	}
}
