package fsnotify

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type testCase struct {
	name string
	ops  func(*testing.T, *Watcher, string)
	want string
}

func (tt testCase) run(t *testing.T) {
	t.Run(tt.name, func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()

		w := newCollector(t)
		w.collect(t)

		tt.ops(t, w.w, tmp)

		cmpEvents(t, tmp, w.stop(t), newEvents(t, tt.want))
	})
}

// We wait a little bit after most commands; gives the system some time to sync
// things and makes things more consistent across platforms.
func eventSeparator() { time.Sleep(50 * time.Millisecond) }
func waitForEvents()  { time.Sleep(500 * time.Millisecond) }

// newWatcher initializes an fsnotify Watcher instance.
func newWatcher(t *testing.T, add ...string) *Watcher {
	t.Helper()
	w, err := NewWatcher()
	if err != nil {
		t.Fatalf("newWatcher: %s", err)
	}
	for _, a := range add {
		err := w.Add(a)
		if err != nil {
			t.Fatalf("newWatcher: add %q: %s", a, err)
		}
	}
	return w
}

// addWatch adds a watch for a directory
func addWatch(t *testing.T, watcher *Watcher, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("addWatch: path must have at least one element: %s", path)
	}
	err := watcher.Add(filepath.Join(path...))
	if err != nil {
		t.Fatalf("addWatch(%q): %s", filepath.Join(path...), err)
	}
}

const noWait = ""

func shouldWait(path ...string) bool {
	// Take advantage of the fact that filepath.Join skips empty parameters.
	for _, p := range path {
		if p == "" {
			return false
		}
	}
	return true
}

// mkdir
func mkdir(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("mkdir: path must have at least one element: %s", path)
	}
	err := os.Mkdir(filepath.Join(path...), 0o0755)
	if err != nil {
		t.Fatalf("mkdir(%q): %s", filepath.Join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// mkdir -p
// func mkdirAll(t *testing.T, path ...string) {
// 	t.Helper()
// 	if len(path) < 1 {
// 		t.Fatalf("mkdirAll: path must have at least one element: %s", path)
// 	}
// 	err := os.MkdirAll(filepath.Join(path...), 0o0755)
// 	if err != nil {
// 		t.Fatalf("mkdirAll(%q): %s", filepath.Join(path...), err)
// 	}
// 	if shouldWait(path...) {
// 		eventSeparator()
// 	}
// }

// ln -s
func symlink(t *testing.T, target string, link ...string) {
	t.Helper()
	if len(link) < 1 {
		t.Fatalf("symlink: link must have at least one element: %s", link)
	}
	err := os.Symlink(target, filepath.Join(link...))
	if err != nil {
		t.Fatalf("symlink(%q, %q): %s", target, filepath.Join(link...), err)
	}
	if shouldWait(link...) {
		eventSeparator()
	}
}

// cat
func cat(t *testing.T, data string, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("cat: path must have at least one element: %s", path)
	}

	err := func() error {
		fp, err := os.OpenFile(filepath.Join(path...), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return err
		}
		if err := fp.Sync(); err != nil {
			return err
		}
		if shouldWait(path...) {
			eventSeparator()
		}
		if _, err := fp.WriteString(data); err != nil {
			return err
		}
		if err := fp.Sync(); err != nil {
			return err
		}
		if shouldWait(path...) {
			eventSeparator()
		}
		return fp.Close()
	}()
	if err != nil {
		t.Fatalf("cat(%q): %s", filepath.Join(path...), err)
	}
}

// touch
func touch(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("touch: path must have at least one element: %s", path)
	}
	fp, err := os.Create(filepath.Join(path...))
	if err != nil {
		t.Fatalf("touch(%q): %s", filepath.Join(path...), err)
	}
	err = fp.Close()
	if err != nil {
		t.Fatalf("touch(%q): %s", filepath.Join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// mv
func mv(t *testing.T, src string, dst ...string) {
	t.Helper()
	if len(dst) < 1 {
		t.Fatalf("mv: dst must have at least one element: %s", dst)
	}

	var err error
	switch runtime.GOOS {
	case "windows", "plan9":
		err = os.Rename(src, filepath.Join(dst...))
	default:
		err = exec.Command("mv", src, filepath.Join(dst...)).Run()
	}
	if err != nil {
		t.Fatalf("mv(%q, %q): %s", src, filepath.Join(dst...), err)
	}
	if shouldWait(dst...) {
		eventSeparator()
	}
}

// rm
func rm(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("rm: path must have at least one element: %s", path)
	}
	err := os.Remove(filepath.Join(path...))
	if err != nil {
		t.Fatalf("rm(%q): %s", filepath.Join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// rm -r
func rmAll(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("rmAll: path must have at least one element: %s", path)
	}
	err := os.RemoveAll(filepath.Join(path...))
	if err != nil {
		t.Fatalf("rmAll(%q): %s", filepath.Join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// chmod
func chmod(t *testing.T, mode fs.FileMode, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("chmod: path must have at least one element: %s", path)
	}
	err := os.Chmod(filepath.Join(path...), mode)
	if err != nil {
		t.Fatalf("chmod(%q): %s", filepath.Join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// Collect all events in an array.
//
// w := newCollector(t)
// w.collect(r)
//
// .. do stuff ..
//
// events := w.stop(t)
type eventCollector struct {
	w      *Watcher
	events Events
	mu     sync.Mutex
	done   chan struct{}
}

func newCollector(t *testing.T) *eventCollector {
	return &eventCollector{w: newWatcher(t), done: make(chan struct{})}
}

func (w *eventCollector) stop(t *testing.T) Events {
	waitForEvents()

	go func() {
		err := w.w.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	select {
	case <-time.After(1 * time.Second):
		t.Fatal("event stream was not closed after 1 second")
	case <-w.done:
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.events
}

func (w *eventCollector) collect(t *testing.T) {
	go func() {
		for {
			select {
			case e, ok := <-w.w.Errors:
				if !ok {
					w.done <- struct{}{}
					return
				}
				t.Error(e)
				return
			case e, ok := <-w.w.Events:
				if !ok {
					w.done <- struct{}{}
					return
				}
				w.mu.Lock()
				w.events = append(w.events, e)
				w.mu.Unlock()
			}
		}
	}()
}

type Events []Event

func (e Events) String() string {
	b := new(strings.Builder)
	for i, ee := range e {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "%-20s %q", ee.Op.String(), filepath.ToSlash(ee.Name))
	}
	return b.String()
}

func (e Events) TrimPrefix(prefix string) Events {
	for i := range e {
		if e[i].Name == prefix {
			e[i].Name = "/"
		} else {
			e[i].Name = strings.TrimPrefix(e[i].Name, prefix)
		}
	}
	return e
}

func (e Events) copy() Events {
	cp := make(Events, len(e))
	copy(cp, e)
	return cp
}

// Create a new Events list from a string; for example:
//
//   CREATE        path
//   CREATE|WRITE  path
//
// Every event is one line, and any whitespace between the event and path are
// ignored. The path can optionally be surrounded in ". Anything after a "#" is
// ignored.
//
// Platform-specific tests can be added after GOOS:
//
//   # Tested if nothing else matches
//   CREATE   path
//
//   # Windows-specific test.
//   windows:
//     WRITE    path
func newEvents(t *testing.T, s string) Events {
	t.Helper()

	var (
		lines  = strings.Split(s, "\n")
		group  string
		events = make(map[string]Events)
	)
	for no, line := range lines {
		if i := strings.IndexByte(line, '#'); i > -1 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, ":") {
			group = strings.TrimRight(line, ":")
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("newEvents: line %d has less than 2 fields: %s", no, line)
		}

		path := strings.Trim(fields[len(fields)-1], `"`)

		var op Op
		for _, e := range fields[:len(fields)-1] {
			if e == "|" {
				continue
			}
			for _, ee := range strings.Split(e, "|") {
				switch strings.ToUpper(ee) {
				case "CREATE":
					op |= Create
				case "WRITE":
					op |= Write
				case "REMOVE":
					op |= Remove
				case "RENAME":
					op |= Rename
				case "CHMOD":
					op |= Chmod
				default:
					t.Fatalf("newEvents: line %d has unknown event %q: %s", no, ee, line)
				}
			}
		}
		events[group] = append(events[group], Event{Name: path, Op: op})
	}

	if e, ok := events[runtime.GOOS]; ok {
		return e
	}
	return events[""]
}

func cmpEvents(t *testing.T, tmp string, have, want Events) {
	t.Helper()

	have = have.TrimPrefix(tmp)

	haveSort, wantSort := have.copy(), want.copy()
	sort.Slice(haveSort, func(i, j int) bool {
		return haveSort[i].String() > haveSort[j].String()
	})
	sort.Slice(wantSort, func(i, j int) bool {
		return wantSort[i].String() > wantSort[j].String()
	})

	if haveSort.String() != wantSort.String() {
		t.Errorf("\nhave:\n%s\nwant:\n%s", indent(have), indent(want))
	}
}

func indent(s fmt.Stringer) string {
	return "\t" + strings.ReplaceAll(s.String(), "\n", "\n\t")
}
