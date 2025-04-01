//go:build windows && usn

package fsnotify

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRemoveState(t *testing.T) {
	if !isAdmin() {
		t.Skip("not admin")
	}
	var (
		tmp        = t.TempDir()
		dir        = join(tmp, "dir")
		file       = join(dir, "file")
		recvEvents = 0
		wantEvents = 3
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-w.b.(*usnBackend).quit:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				recvEvents++
				t.Logf("event: %v", ev)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				t.Errorf("watcher error: %v", err)
			}
		}
	}()

	addWatch(t, w, tmp)
	addWatch(t, w, file)

	touch(t, file)
	echo(t, false, "test", file)
	rm(t, file)

	check := func(want int) {
		t.Helper()
		if len(w.b.(*usnBackend).paths) != want {
			var d []string
			for k, v := range w.b.(*usnBackend).paths {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches (have %d, want %d):\n%v",
				len(w.b.(*usnBackend).paths), want, strings.Join(d, "\n"))
		}
	}

	check(2)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal("should not change internal state")
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

	w.Close()

	wg.Wait()

	if recvEvents != wantEvents {
		t.Errorf("recvEvents = %d, want %d", recvEvents, wantEvents)
	}
}

func TestUSNRemWatch(t *testing.T) {
	if !isAdmin() {
		t.Skip("not admin")
	}
	tmp := t.TempDir()

	touch(t, tmp, "file")

	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, tmp)
	if err := w.Remove(tmp); err != nil {
		t.Fatalf("Could not remove the watch: %v\n", err)
	}

	// Verify that the watch was properly removed and handles were closed
	usn := w.b.(*usnBackend)
	usn.mu.Lock()
	if _, ok := usn.paths[tmp]; ok {
		t.Fatal("Watch was not removed from the watches map")
	}
	usn.mu.Unlock()
}

func TestUSNClose(t *testing.T) {
	if !isAdmin() {
		t.Skip("not admin")
	}
	tmp := t.TempDir()
	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, tmp)

	// Close the watcher
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify that all watches were removed and handles were closed
	usn := w.b.(*usnBackend)
	usn.mu.Lock()
	if len(usn.paths) != 0 {
		t.Errorf("Expected 0 watches after close, got %d", len(usn.paths))
	}
	if len(usn.volumes) != 0 {
		t.Errorf("Expected 0 volumes after close, got %d", len(usn.volumes))
	}
	usn.mu.Unlock()

	// Verify that the watcher is marked as closed
	if !usn.isClosed() {
		t.Error("Watcher was not marked as closed")
	}
}

// isAdmin checks if the current user has Administrator privileges.
func isAdmin() bool {
	var sid *windows.SID
	// Create a SID for the Administrators group.
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	// Get the current process token.
	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}
