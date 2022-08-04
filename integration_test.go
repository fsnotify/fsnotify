//go:build !plan9
// +build !plan9

package fsnotify

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatch(t *testing.T) {
	tests := []testCase{
		{"multiple creates", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			addWatch(t, w, tmp)

			cat(t, "data", file)
			rm(t, file)

			touch(t, file)       // Recreate the file
			cat(t, "data", file) // Modify
			cat(t, "data", file) // Modify
		}, `
			create  /file
			write   /file
			remove  /file
			create  /file
			write   /file
			write   /file
		`},

		{"dir only", func(t *testing.T, w *Watcher, tmp string) {
			beforeWatch := filepath.Join(tmp, "beforewatch")
			file := filepath.Join(tmp, "file")

			touch(t, beforeWatch)
			addWatch(t, w, tmp)

			cat(t, "data", file)
			rm(t, file)
			rm(t, beforeWatch)
		}, `
			create /file
			write  /file
			remove /file
			remove /beforewatch
		`},

		{"subdir", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)

			file := filepath.Join(tmp, "file")
			dir := filepath.Join(tmp, "sub")
			dirfile := filepath.Join(tmp, "sub/file2")

			mkdir(t, dir)     // Create sub-directory
			touch(t, file)    // Create a file
			touch(t, dirfile) // Create a file (Should not see this! we are not watching subdir)
			time.Sleep(200 * time.Millisecond)
			rmAll(t, dir) // Make sure receive deletes for both file and sub-directory
			rm(t, file)
		}, `
			create /sub
			create /file
			remove /sub
			remove /file

			# Windows includes a write for the /sub dir too, two of them even(?)
			windows:
				create /sub
				create /file
				write  /sub
				write  /sub
				remove /sub
				remove /file
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchRename(t *testing.T) {
	tests := []testCase{
		{"rename file", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")

			addWatch(t, w, tmp)
			cat(t, "asd", file)
			mv(t, file, tmp, "renamed")
		}, `
			create /file
			write  /file
			rename /file
			create /renamed
		`},

		{"rename from unwatched directory", func(t *testing.T, w *Watcher, tmp string) {
			unwatched := t.TempDir()

			addWatch(t, w, tmp)
			touch(t, unwatched, "file")
			mv(t, filepath.Join(unwatched, "file"), tmp, "file")
		}, `
			create /file
		`},

		{"rename to unwatched directory", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "netbsd" {
				t.Skip("NetBSD behaviour is not fully correct") // TODO: investigate and fix.
			}

			unwatched := t.TempDir()
			file := filepath.Join(tmp, "file")
			renamed := filepath.Join(unwatched, "renamed")

			addWatch(t, w, tmp)

			cat(t, "data", file)
			mv(t, file, renamed)
			cat(t, "data", renamed) // Modify the file outside of the watched dir
			touch(t, file)          // Recreate the file that was moved
		}, `
			create /file
			write  /file
			rename /file
			create /file

			# Windows has REMOVE /file, rather than CREATE /file
			windows:
				create   /file
				write    /file
				remove   /file
				create   /file
		`},

		{"rename overwriting existing file", func(t *testing.T, w *Watcher, tmp string) {
			switch runtime.GOOS {
			case "windows":
				t.Skipf("os.Rename over existing file does not create an event on %q", runtime.GOOS)
			}

			touch(t, tmp, "renamed")
			addWatch(t, w, tmp)

			unwatched := t.TempDir()
			file := filepath.Join(unwatched, "file")
			touch(t, file)
			mv(t, file, tmp, "renamed")
		}, `
			remove /renamed
			create /renamed

			# No remove event for Windows and Linux.
			linux:
				create /renamed
			windows:
				create /renamed
		`},

		{"rename watched directory", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)

			dir := filepath.Join(tmp, "dir")
			mkdir(t, dir)
			addWatch(t, w, dir)

			mv(t, dir, tmp, "dir-renamed")
			touch(t, tmp, "dir-renamed/file")
		}, `
			CREATE   "/dir"           # mkdir
			RENAME   "/dir"           # mv
			CREATE   "/dir-renamed"
			RENAME   "/dir"
			CREATE   "/dir/file"      # touch

			windows:
				CREATE       "/dir"                 # mkdir
				RENAME       "/dir"                 # mv
				CREATE       "/dir-renamed"
				CREATE       "/dir-renamed/file"    # touch
			# TODO: no results for the touch; this is probably a bug; windows
			# was fixed in #370.
			kqueue:
				CREATE               "/dir"           # mkdir
				CREATE               "/dir-renamed"   # mv
				REMOVE|RENAME        "/dir"

			# We don't currently have a way to realize that the CREATEd directory is the one that was RENAMEd,
			# so we don't know we need to watch it for new files.
			# Possibly similar to the issue with kqueue??
			solaris:
				CREATE       "/dir"                 # mkdir
				RENAME       "/dir"                 # mv
				CREATE       "/dir-renamed"

		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks don't work on Windows")
	}

	tests := []testCase{
		{"create unresolvable symlink", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)

			symlink(t, filepath.Join(tmp, "target"), tmp, "link")
		}, `
			create /link
		`},

		{"cyclic symlink", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "darwin" {
				// This test is borked on macOS; it reports events outside the
				// watched directory:
				//
				//   create "/private/.../testwatchsymlinkcyclic_symlink3681444267/001/link"
				//   create "/link"
				//   write  "/link"
				//   write  "/private/.../testwatchsymlinkcyclic_symlink3681444267/001/link"
				//
				// kqueue.go does a lot of weird things with symlinks that I
				// don't think are necessarily correct, but need to test a bit
				// more.
				t.Skip()
			}

			symlink(t, ".", tmp, "link")
			addWatch(t, w, tmp)
			rm(t, tmp, "link")
			cat(t, "foo", tmp, "link")

		}, `
			write  /link
			create /link

			linux:
				remove    /link
				create    /link
				write     /link
			solaris:
				remove    /link
				create    /link
				write     /link
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchAttrib(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("attributes don't work on Windows")
	}

	tests := []testCase{
		{"chmod", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")

			cat(t, "data", file)
			addWatch(t, w, file)
			chmod(t, 0o700, file)
		}, `
			CHMOD   "/file"
		`},

		{"write does not trigger CHMOD", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")

			cat(t, "data", file)
			addWatch(t, w, file)
			chmod(t, 0o700, file)

			cat(t, "more data", file)
		}, `
			CHMOD   "/file"
			WRITE   "/file"
		`},

		{"chmod after write", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")

			cat(t, "data", file)
			addWatch(t, w, file)
			chmod(t, 0o700, file)
			cat(t, "more data", file)
			chmod(t, 0o600, file)
		}, `
			CHMOD   "/file"
			WRITE   "/file"
			CHMOD   "/file"
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchRm(t *testing.T) {
	tests := []testCase{
		{"remove watched directory", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "openbsd" || runtime.GOOS == "netbsd" {
				t.Skip("behaviour is inconsistent on OpenBSD and NetBSD, and this test is flaky")
			}

			file := filepath.Join(tmp, "file")

			touch(t, file)
			addWatch(t, w, tmp)
			rmAll(t, tmp)
		}, `
			# OpenBSD, NetBSD
			remove             /file
			remove|write       /

			freebsd:
				remove|write   "/"
				remove         ""
				create         "."

			darwin:
				remove         /file
				remove|write   /
			linux:
				remove         /file
				remove         /
			windows:
				remove         /file
				remove         /
			solaris:
				remove         /file
				remove         /
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestClose(t *testing.T) {
	t.Run("close", func(t *testing.T) {
		t.Parallel()

		w := newWatcher(t)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		var done int32
		go func() {
			w.Close()
			atomic.StoreInt32(&done, 1)
		}()

		eventSeparator()
		if atomic.LoadInt32(&done) == 0 {
			t.Fatal("double Close() test failed: second Close() call didn't return")
		}

		if err := w.Add(t.TempDir()); err == nil {
			t.Fatal("expected error on Watch() after Close(), got nil")
		}
	})

	// Make sure that Close() works even when the Events channel isn't being
	// read.
	t.Run("events not read", func(t *testing.T) {
		t.Parallel()

		tmp := t.TempDir()
		w := newWatcher(t, tmp)

		touch(t, tmp, "file")
		rm(t, tmp, "file")
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	})

	// Make sure that calling Close() while REMOVE events are emitted doesn't race.
	t.Run("close while removing files", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()

		files := make([]string, 0, 200)
		for i := 0; i < 200; i++ {
			f := filepath.Join(tmp, fmt.Sprintf("file-%03d", i))
			touch(t, f, noWait)
			files = append(files, f)
		}

		w := newWatcher(t, tmp)

		startC, stopC, errC := make(chan struct{}), make(chan struct{}), make(chan error)
		go func() {
			for {
				select {
				case <-w.Errors:
				case <-w.Events:
				case <-stopC:
					return
				}
			}
		}()
		rmDone := make(chan struct{})
		go func() {
			<-startC
			for _, f := range files {
				rm(t, f, noWait)
			}
			rmDone <- struct{}{}
		}()
		go func() {
			<-startC
			errC <- w.Close()
		}()
		close(startC)
		defer close(stopC)
		if err := <-errC; err != nil {
			t.Fatal(err)
		}

		<-rmDone
	})

	// Make sure Close() doesn't race when called more than once; hard to write
	// a good reproducible test for this, but running it 150 times seems to
	// reproduce it in ~75% of cases and isn't too slow (~0.06s on my system).
	t.Run("double close", func(t *testing.T) {
		t.Parallel()

		for i := 0; i < 150; i++ {
			w, err := NewWatcher()
			if err != nil {
				if strings.Contains(err.Error(), "too many") { // syscall.EMFILE
					time.Sleep(100 * time.Millisecond)
					continue
				}
				t.Fatal(err)
			}
			go w.Close()
			go w.Close()
			go w.Close()
		}
	})
}

// TODO: should also check internal state is correct/cleaned up; e.g. no
//       left-over file descriptors or whatnot.
func TestRemove(t *testing.T) {
	t.Run("works", func(t *testing.T) {
		t.Parallel()

		tmp := t.TempDir()
		touch(t, tmp, "file")

		w := newCollector(t)
		w.collect(t)
		addWatch(t, w.w, tmp)
		if err := w.w.Remove(tmp); err != nil {
			t.Fatal(err)
		}

		time.Sleep(200 * time.Millisecond)
		cat(t, "data", tmp, "file")
		chmod(t, 0o700, tmp, "file")

		have := w.stop(t)
		if len(have) > 0 {
			t.Errorf("received events; expected none:\n%s", have)
		}
	})

	t.Run("remove same dir twice", func(t *testing.T) {
		tmp := t.TempDir()

		touch(t, tmp, "file")

		w := newWatcher(t)
		defer w.Close()

		addWatch(t, w, tmp)

		if err := w.Remove(tmp); err != nil {
			t.Fatal(err)
		}
		if err := w.Remove(tmp); err == nil {
			t.Fatal("no error")
		}
	})

	// Make sure that concurrent calls to Remove() don't race.
	t.Run("no race", func(t *testing.T) {
		t.Parallel()

		tmp := t.TempDir()
		touch(t, tmp, "file")

		for i := 0; i < 10; i++ {
			w := newWatcher(t)
			defer w.Close()
			addWatch(t, w, tmp)

			done := make(chan struct{})
			go func() {
				defer func() { done <- struct{}{} }()
				w.Remove(tmp)
			}()
			go func() {
				defer func() { done <- struct{}{} }()
				w.Remove(tmp)
			}()
			<-done
			<-done
			w.Close()
		}
	})
}
