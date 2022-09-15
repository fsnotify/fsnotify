//go:build !plan9 && !solaris
// +build !plan9,!solaris

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify/internal"
)

// Set soft open file limit to the maximum; on e.g. OpenBSD it's 512/1024.
//
// Go 1.19 will always do this when the os package is imported.
//
// https://go-review.googlesource.com/c/go/+/393354/
func init() {
	internal.SetRlimit()
}

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

			# TODO: not sure why the REMOVE /sub is dropped.
			dragonfly:
				create    /sub
				create    /file
				remove    /file

			# Windows includes a write for the /sub dir too, two of them even(?)
			windows:
				create /sub
				create /file
				write  /sub
				write  /sub
				remove /sub
				remove /file
		`},

		{"file in directory is not readable", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "windows" {
				t.Skip("attributes don't work on Windows")
			}

			touch(t, tmp, "file-unreadable")
			chmod(t, 0, tmp, "file-unreadable")
			touch(t, tmp, "file")
			addWatch(t, w, tmp)

			cat(t, "hello", tmp, "file")
			rm(t, tmp, "file")
			rm(t, tmp, "file-unreadable")
		}, `
			WRITE     "/file"
			REMOVE    "/file"
			REMOVE    "/file-unreadable"

			# We never set up a watcher on the unreadable file, so we don't get
			# the REMOVE.
			kqueue:
				WRITE    "/file"
				REMOVE   "/file"
		`},

		{"watch same dir twice", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)
			addWatch(t, w, tmp)

			touch(t, tmp, "file")
			cat(t, "hello", tmp, "file")
			rm(t, tmp, "file")
			mkdir(t, tmp, "dir")
		}, `
			create   /file
			write    /file
			remove   /file
			create   /dir
		`},

		{"watch same file twice", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			touch(t, file)

			addWatch(t, w, file)
			addWatch(t, w, file)

			cat(t, "hello", tmp, "file")
		}, `
			write    /file
		`},

		{"watch a symlink to a file", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "darwin" {
				// TODO
				// WRITE "/private/var/folders/.../TestWatchwatch_a_symlink_to_a_file183391030/001/file"
				// Pretty sure this is caused by the broken symlink-follow
				// behaviour too.
				t.Skip("broken on macOS")
			}

			file := filepath.Join(tmp, "file")
			link := filepath.Join(tmp, "link")
			touch(t, file)
			symlink(t, file, link)
			addWatch(t, w, link)

			cat(t, "hello", file)
		}, `
			write    /link

			# TODO: Symlinks followed on kqueue; it shouldn't do this, but I'm
			# afraid changing it will break stuff. See #227, #390
			kqueue:
				write    /file

			# TODO: see if we can fix this.
			windows:
				empty
		`},

		{"watch a symlink to a dir", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "darwin" {
				// TODO
				// CREATE "/private/var/.../TestWatchwatch_a_symlink_to_a_dir2551725268/001/dir/file"
				// Pretty sure this is caused by the broken symlink-follow
				// behaviour too.

				t.Skip("broken on macOS")
			}

			dir := filepath.Join(tmp, "dir")
			link := filepath.Join(tmp, "link")
			mkdir(t, dir)
			symlink(t, dir, link)
			addWatch(t, w, link)

			touch(t, dir, "file")
		}, `
			create    /link/file

			# TODO: Symlinks followed on kqueue; it shouldn't do this, but I'm
			# afraid changing it will break stuff. See #227, #390
			kqueue:
				create /dir/file
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchCreate(t *testing.T) {
	tests := []testCase{
		// Files
		{"create empty file", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)
			touch(t, tmp, "file")
		}, `
			create  /file
		`},
		{"create file with data", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)
			cat(t, "data", tmp, "file")
		}, `
			create  /file
			write   /file
		`},

		// Directories
		{"create new directory", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)
			mkdir(t, tmp, "dir")
		}, `
			create  /dir
		`},

		// Links
		{"create new symlink to file", func(t *testing.T, w *Watcher, tmp string) {
			touch(t, tmp, "file")
			addWatch(t, w, tmp)
			symlink(t, filepath.Join(tmp, "file"), tmp, "link")
		}, `
			create  /link

			windows:
				create   /link
				write    /link
		`},
		{"create new symlink to directory", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)
			symlink(t, tmp, tmp, "link")
		}, `
			create  /link

			windows:
				create  /link
				write  /link
		`},

		// FIFO
		{"create new named pipe", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "windows" {
				t.Skip("no named pipes on windows")
			}
			touch(t, tmp, "file")
			addWatch(t, w, tmp)
			mkfifo(t, tmp, "fifo")
		}, `
			create  /fifo
		`},
		// Device node
		{"create new device node pipe", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "windows" {
				t.Skip("no device nodes on windows")
			}
			if isKqueue() {
				t.Skip("needs root on BSD")
			}
			touch(t, tmp, "file")
			addWatch(t, w, tmp)

			mknod(t, 0, tmp, "dev")
		}, `
			create  /dev
		`},
	}
	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchWrite(t *testing.T) {
	tests := []testCase{
		// Files
		{"truncate file", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			cat(t, "data", file)
			addWatch(t, w, tmp)

			fp, err := os.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := fp.Sync(); err != nil {
				t.Fatal(err)
			}
			eventSeparator()
			if _, err := fp.Write([]byte("X")); err != nil {
				t.Fatal(err)
			}
			if err := fp.Close(); err != nil {
				t.Fatal(err)
			}
		}, `
			write  /file  # truncate
			write  /file  # write

			# Truncate is chmod on kqueue, except NetBSD
			netbsd:
				write  /file
			kqueue:
				chmod     /file
				write     /file
		`},

		{"multiple writes to a file", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			cat(t, "data", file)
			addWatch(t, w, tmp)

			fp, err := os.OpenFile(file, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fp.Write([]byte("X")); err != nil {
				t.Fatal(err)
			}
			if err := fp.Sync(); err != nil {
				t.Fatal(err)
			}
			eventSeparator()
			if _, err := fp.Write([]byte("Y")); err != nil {
				t.Fatal(err)
			}
			if err := fp.Close(); err != nil {
				t.Fatal(err)
			}
		}, `
			write  /file  # write X
			write  /file  # write Y
		`},
	}
	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchRename(t *testing.T) {
	tests := []testCase{
		{"rename file in watched dir", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			cat(t, "asd", file)

			addWatch(t, w, tmp)
			mv(t, file, tmp, "renamed")
		}, `
			rename /file
			create /renamed
		`},

		{"rename from unwatched dir", func(t *testing.T, w *Watcher, tmp string) {
			unwatched := t.TempDir()

			addWatch(t, w, tmp)
			touch(t, unwatched, "file")
			mv(t, filepath.Join(unwatched, "file"), tmp, "file")
		}, `
			create /file
		`},

		{"rename to unwatched dir", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "netbsd" && isCI() {
				t.Skip("fails in CI; see #488")
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
			create /file # cat data >file
			write  /file # ^
			rename /file # mv file ../renamed
			create /file # touch file

			# Windows has REMOVE /file, rather than CREATE /file
			windows:
				create   /file
				write    /file
				remove   /file
				create   /file
		`},

		{"rename overwriting existing file", func(t *testing.T, w *Watcher, tmp string) {
			unwatched := t.TempDir()
			file := filepath.Join(unwatched, "file")

			touch(t, tmp, "renamed")
			touch(t, file)

			addWatch(t, w, tmp)
			mv(t, file, tmp, "renamed")
		}, `
			# TODO: this should really be RENAME.
			remove /renamed
			create /renamed

			# No remove event for inotify; inotify just sends MOVE_SELF.
			linux:
				create /renamed

			# TODO: this is broken.
			dragonfly:
				REMOVE|WRITE         "/"
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
		`},

		{"rename watched file", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			rename := filepath.Join(tmp, "rename-one")
			touch(t, file)

			addWatch(t, w, file)

			mv(t, file, rename)
			mv(t, rename, tmp, "rename-two")
		}, `
			# TODO: this should update the path. And even then, not clear what
			# go renamed to what.
			rename /file  # mv file rename
			rename /file  # mv rename rename-two

			# TODO: seems to lose the watch?
			kqueue:
				rename     /file

			# It's actually more correct on Windows.
			windows:
				rename     /file
				rename     /rename-one
		`},

		{"re-add renamed file", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			rename := filepath.Join(tmp, "rename")
			touch(t, file)

			addWatch(t, w, file)

			mv(t, file, rename)
			touch(t, file)
			addWatch(t, w, file)
			cat(t, "hello", rename)
			cat(t, "hello", file)
		}, `
			rename /file    # mv file rename
			write  /rename  # cat hello >rename
			write  /file    # cat hello >file

			# TODO: wrong.
			linux:
			    RENAME     "/file"
			    WRITE      "/file"
			    WRITE      ""

			# TODO: wrong.
			kqueue:
			   RENAME      "/file"
			   WRITE       "/file"
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestWatchSymlink(t *testing.T) {
	tests := []testCase{
		{"create unresolvable symlink", func(t *testing.T, w *Watcher, tmp string) {
			addWatch(t, w, tmp)

			symlink(t, filepath.Join(tmp, "target"), tmp, "link")
		}, `
			create /link

			windows:
				create    /link
				write     /link

			# No events at all on Dragonfly
			# TODO: should fix this.
			dragonfly:
				empty
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

			linux, windows:
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
		{"remove watched file", func(t *testing.T, w *Watcher, tmp string) {
			file := filepath.Join(tmp, "file")
			touch(t, file)

			addWatch(t, w, file)
			rm(t, file)
		}, `
			REMOVE   "/file"

			# unlink always emits a CHMOD on Linux.
			linux:
				CHMOD    "/file"
				REMOVE   "/file"
		`},

		{"remove watched file with open fd", func(t *testing.T, w *Watcher, tmp string) {
			if runtime.GOOS == "windows" {
				t.Skip("Windows hard-locks open files so this will never work")
			}

			file := filepath.Join(tmp, "file")
			touch(t, file)

			// Intentionally don't close the descriptor here so it stays around.
			_, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}

			addWatch(t, w, file)
			rm(t, file)
		}, `
			REMOVE   "/file"

			# inotify will just emit a CHMOD for the unlink, but won't actually
			# emit a REMOVE until the descriptor is closed. Bit odd, but not much
			# we can do about it. The REMOVE is tested in TestInotifyDeleteOpenFile()
			linux:
				CHMOD    "/file"
		`},

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
		`},
	}

	for _, tt := range tests {
		tt := tt
		tt.run(t)
	}
}

func TestClose(t *testing.T) {
	chanClosed := func(t *testing.T, w *Watcher) {
		t.Helper()

		// Need a small sleep as Close() on kqueue does all sorts of things,
		// which may take a little bit.
		switch runtime.GOOS {
		case "freebsd", "openbsd", "netbsd", "dragonfly", "darwin":
			time.Sleep(5 * time.Millisecond)
		}

		select {
		default:
			t.Fatal("blocking on Events")
		case _, ok := <-w.Events:
			if ok {
				t.Fatal("Events not closed")
			}
		}
		select {
		default:
			t.Fatal("blocking on Errors")
		case _, ok := <-w.Errors:
			if ok {
				t.Fatal("Errors not closed")
			}
		}
	}

	t.Run("close", func(t *testing.T) {
		t.Parallel()

		w := newWatcher(t)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		chanClosed(t, w)

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

		// TODO: windows backend doesn't work well here; can't easily fix it.
		//       Need to rewrite things a bit.
		if runtime.GOOS != "windows" {
			chanClosed(t, w)
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

	t.Run("closes channels after read", func(t *testing.T) {
		if runtime.GOOS == "netbsd" {
			t.Skip("flaky") // TODO
		}

		t.Parallel()

		tmp := t.TempDir()

		w := newCollector(t, tmp)
		w.collect(t)
		touch(t, tmp, "qwe")
		touch(t, tmp, "asd")

		if err := w.w.Close(); err != nil {
			t.Fatal(err)
		}

		chanClosed(t, w.w)
	})
}

func TestAdd(t *testing.T) {
	t.Run("permission denied", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("attributes don't work on Windows")
		}

		t.Parallel()

		tmp := t.TempDir()
		dir := filepath.Join(tmp, "dir-unreadable")
		mkdir(t, dir)
		touch(t, dir, "/file")
		chmod(t, 0, dir)

		w := newWatcher(t)
		defer func() {
			w.Close()
			chmod(t, 0o755, dir) // Make TempDir() cleanup work
		}()
		err := w.Add(dir)
		if err == nil {
			t.Fatal("error is nil")
		}
		if !errors.Is(err, internal.UnixEACCES) {
			t.Errorf("not unix.EACCESS: %T %#[1]v", err)
		}
		if !errors.Is(err, internal.SyscallEACCES) {
			t.Errorf("not syscall.EACCESS: %T %#[1]v", err)
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
		t.Parallel()

		tmp := t.TempDir()

		touch(t, tmp, "file")

		w := newWatcher(t)
		defer w.Close()

		addWatch(t, w, tmp)

		if err := w.Remove(tmp); err != nil {
			t.Fatal(err)
		}
		err := w.Remove(tmp)
		if err == nil {
			t.Fatal("no error")
		}
		if !errors.Is(err, ErrNonExistentWatch) {
			t.Fatalf("wrong error: %T", err)
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

func TestEventString(t *testing.T) {
	tests := []struct {
		in   Event
		want string
	}{
		{Event{}, `[no events]   ""`},
		{Event{"/file", 0}, `[no events]   "/file"`},

		{Event{"/file", Chmod | Create},
			`CREATE|CHMOD  "/file"`},
		{Event{"/file", Rename},
			`RENAME        "/file"`},
		{Event{"/file", Remove},
			`REMOVE        "/file"`},
		{Event{"/file", Write | Chmod},
			`WRITE|CHMOD   "/file"`},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			have := tt.in.String()
			if have != tt.want {
				t.Errorf("\nhave: %q\nwant: %q", have, tt.want)
			}
		})
	}
}

func isKqueue() bool {
	switch runtime.GOOS {
	case "linux", "windows":
		return false
	}
	return true
}

// Verify the watcher can keep up with file creations/deletions when under load.
func TestWatchStress(t *testing.T) {
	// On NetBSD ioutil.ReadDir in sendDirectoryChangeEvents() returns EINVAL
	// ~80% of the time:
	//
	//    readdirent /tmp/TestWatchStress3584363325/001: invalid argument
	//
	// This ends up calling getdents(), the manpage says:
	//
	// [EINVAL]  A directory was being read on NFS, but it was modified on the
	//           server while it was being read.
	//
	// Which is, eh, odd? Maybe I read the code wrong and it's calling another
	// function too(?)
	//
	// Because this happens on the Errors channel we can't "skip" it like with
	// other kqueue platorms, so just skip the entire test for now.
	//
	// TODO: fix this.
	if runtime.GOOS == "netbsd" {
		t.Skip("broken on NetBSD")
	}

	Errorf := func(t *testing.T, msg string, args ...interface{}) {
		if !isKqueue() {
			t.Errorf(msg, args...)
			return
		}

		// On kqueue platforms it doesn't seem to sync properly; see comment for
		// the sleep below.
		//
		// TODO: fix this.
		t.Logf(msg, args...)
		t.Skip("flaky on kqueue; allowed to fail")
	}

	tmp := t.TempDir()
	w := newCollector(t, tmp)
	w.collect(t)

	fmtNum := func(n int) string {
		s := fmt.Sprintf("%09d", n)
		return s[:3] + "_" + s[3:6] + "_" + s[6:]
	}

	var (
		numFiles = 1_500_000
		runFor   = 30 * time.Second
	)
	if testing.Short() {
		runFor = time.Second
	}

	// Otherwise platforms with low limits such as as OpenBSD and NetBSD will
	// fail, since every watched file uses a file descriptor. Need superuser
	// permissions and twiddling with /etc/login.conf to adjust them, so we
	// can't "just increase it".
	if isKqueue() && uint64(numFiles) > internal.Maxfiles() {
		numFiles = int(internal.Maxfiles()) - 100
		t.Logf("limiting files to %d due to max open files limit", numFiles)
	}

	var (
		prefix = "xyz-prefix-"
		done   = make(chan struct{})
	)
	// testing.Short()
	go func() {
		numFiles = createFiles(t, tmp, prefix, numFiles, runFor)

		// TODO: this shouldn't be needed; and if this is too short some very
		//       odd events happen:
		//
		//         fsnotify_test.go:837: saw 42 unexpected events:
		//             REMOVE               ""
		//             CREATE               "."
		//             REMOVE               ""
		//             CREATE               "."
		//             REMOVE               ""
		//             ...
		//
		//         fsnotify_test.go:848: expected the following 3175 events, but didn't see them (showing first 100 only)
		//             REMOVE               "/xyz-prefix-000_015_080"
		//             REMOVE               "/xyz-prefix-000_014_536"
		//             CREATE               "/xyz-prefix-000_015_416"
		//             CREATE               "/xyz-prefix-000_015_406"
		//             ...
		//
		// Should really add a Sync() method which processes all outstanding
		// events.
		if isKqueue() {
			time.Sleep(1000 * time.Millisecond)
			if !testing.Short() {
				time.Sleep(1000 * time.Millisecond)
			}
		}

		for i := 0; i < numFiles; i++ {
			rm(t, tmp, prefix+fmtNum(i), noWait)
		}
		close(done)
	}()
	<-done

	have := w.stopWait(t, 10*time.Second)

	// Do some work to get reasonably nice error reports; what cmpEvents() gives
	// us is nice if you have just a few events, but with thousands it qiuckly
	// gets unwieldy.

	want := make(map[Event]struct{})
	for i := 0; i < numFiles; i++ {
		n := "/" + prefix + fmtNum(i)
		want[Event{Name: n, Op: Remove}] = struct{}{}
		want[Event{Name: n, Op: Create}] = struct{}{}
	}

	var extra Events
	for _, h := range have {
		h.Name = filepath.ToSlash(strings.TrimPrefix(h.Name, tmp))
		_, ok := want[h]
		if ok {
			delete(want, h)
		} else {
			extra = append(extra, h)
		}
	}

	if len(extra) > 0 {
		if len(extra) > 100 {
			Errorf(t, "saw %d unexpected events (showing first 100 only):\n%s", len(extra), extra[:100])
		} else {
			Errorf(t, "saw %d unexpected events:\n%s", len(extra), extra)
		}
	}

	if len(want) != 0 {
		wantE := make(Events, 0, len(want))
		for k := range want {
			wantE = append(wantE, k)
		}

		if len(wantE) > 100 {
			Errorf(t, "expected the following %d events, but didn't see them (showing first 100 only)\n%s", len(wantE), wantE[:100])
		} else {
			Errorf(t, "expected the following %d events, but didn't see them\n%s", len(wantE), wantE)
		}
	}
}

func TestWatchList(t *testing.T) {
	if runtime.GOOS == "windows" {
		// TODO: probably should I guess...
		t.Skip("WatchList has always beek broken on Windows and I don't feel like fixing it")
	}

	t.Parallel()

	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")
	other := filepath.Join(tmp, "other")

	touch(t, file)
	touch(t, other)

	w := newWatcher(t, file, tmp)
	defer w.Close()

	have := w.WatchList()
	sort.Strings(have)
	want := []string{tmp, file}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("\nhave: %s\nwant: %s", have, want)
	}
}
