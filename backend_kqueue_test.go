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

func TestKqueueConcurrentAddRemove(t *testing.T) {
	countFDs := func() int {
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			return -1
		}
		return len(entries)
	}

	fdsBefore := countFDs()

	tmp := t.TempDir()
	const numFiles = 16
	files := make([]string, numFiles)
	for i := range numFiles {
		files[i] = join(tmp, fmt.Sprintf("file-%d", i))
		touch(t, files[i])
	}

	w := newWatcher(t)
	kq := w.b.(*kqueue)

	var wg sync.WaitGroup
	for i := range numFiles {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			for range 250 {
				if err := w.Add(path); err != nil {
					t.Errorf("Add(%s) failed: %v", path, err)
					return
				}
				if err := w.Remove(path); err != nil {
					t.Errorf("Remove(%s) failed: %v", path, err)
					return
				}
			}
		}(files[i])
	}
	wg.Wait()

	if t.Failed() {
		w.Close()
		return
	}

	kq.watches.mu.RLock()
	lenPath := len(kq.watches.path)
	lenWd := len(kq.watches.wd)
	lenUser := len(kq.watches.byUser)
	kq.watches.mu.RUnlock()

	if lenPath != 0 || lenWd != 0 || lenUser != 0 {
		t.Fatalf("dangling watches: path=%d wd=%d byUser=%d", lenPath, lenWd, lenUser)
	}

	// Add watches concurrently, verify table integrity, then remove concurrently.
	for i := range numFiles {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if err := w.Add(path); err != nil {
				t.Errorf("Add(%s) failed: %v", path, err)
			}
		}(files[i])
	}
	wg.Wait()

	if t.Failed() {
		w.Close()
		return
	}

	kq.watches.mu.RLock()
	if len(kq.watches.path) != numFiles || len(kq.watches.wd) != numFiles {
		t.Errorf("mismatched table sizes: path=%d wd=%d want=%d",
			len(kq.watches.path), len(kq.watches.wd), numFiles)
	}
	for p, fd := range kq.watches.path {
		info, ok := kq.watches.wd[fd]
		if !ok {
			t.Errorf("missing wd entry for fd %d (path %s)", fd, p)
		} else if info.name != p {
			t.Errorf("mismatched watch path for fd %d: have %s, want %s", fd, info.name, p)
		}
	}
	kq.watches.mu.RUnlock()

	for i := range numFiles {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if err := w.Remove(path); err != nil {
				t.Errorf("Remove(%s) failed: %v", path, err)
			}
		}(files[i])
	}
	wg.Wait()

	if t.Failed() {
		w.Close()
		return
	}

	kq.watches.mu.RLock()
	lenPath = len(kq.watches.path)
	lenWd = len(kq.watches.wd)
	lenUser = len(kq.watches.byUser)
	kq.watches.mu.RUnlock()

	if lenPath != 0 || lenWd != 0 || lenUser != 0 {
		t.Errorf("uncleaned watches after remove: path=%d wd=%d byUser=%d", lenPath, lenWd, lenUser)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	fdsAfter := countFDs()
	if fdsBefore != -1 && fdsAfter != -1 && fdsAfter > fdsBefore {
		t.Errorf("file descriptor leak: before %d, after %d", fdsBefore, fdsAfter)
	}
}
