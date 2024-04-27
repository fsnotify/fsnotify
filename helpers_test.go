package fsnotify

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify/internal"
)

// We wait a little bit after most commands; gives the system some time to sync
// things and makes things more consistent across platforms.
func eventSeparator() { time.Sleep(50 * time.Millisecond) }
func waitForEvents()  { time.Sleep(500 * time.Millisecond) }

// To test the buffered watcher we run the tests twice in the CI: once as "go
// test" and once with FSNOTIFY_BUFFER set. This is a bit hacky, but saves
// having to refactor a lot of this code. Besides, running the tests in the CI
// more than once isn't a bad thing, since it helps catch flaky tests (should
// probably run it even more).
var testBuffered = func() uint {
	s, ok := os.LookupEnv("FSNOTIFY_BUFFER")
	if ok {
		i, err := strconv.ParseUint(s, 0, 0)
		if err != nil {
			panic(fmt.Sprintf("FSNOTIFY_BUFFER: %s", err))
		}
		return uint(i)
	}
	return 0
}()

// newWatcher initializes an fsnotify Watcher instance.
func newWatcher(t *testing.T, add ...string) *Watcher {
	t.Helper()

	var (
		w   *Watcher
		err error
	)
	if testBuffered > 0 {
		w, err = NewBufferedWatcher(testBuffered)
	} else {
		w, err = NewWatcher()
	}
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
func addWatch(t *testing.T, w *Watcher, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("addWatch: path must have at least one element: %s", path)
	}
	err := w.Add(join(path...))
	if err != nil {
		t.Fatalf("addWatch(%q): %s", join(path...), err)
	}
}

// rmWatch removes a watch.
func rmWatch(t *testing.T, watcher *Watcher, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("rmWatch: path must have at least one element: %s", path)
	}
	err := watcher.Remove(join(path...))
	if err != nil {
		t.Fatalf("rmWatch(%q): %s", join(path...), err)
	}
}

const noWait = ""

func shouldWait(path ...string) bool {
	// Take advantage of the fact that join skips empty parameters.
	for _, p := range path {
		if p == "" {
			return false
		}
	}
	return true
}

// Create n empty files with the prefix in the directory dir.
func createFiles(t *testing.T, dir, prefix string, n int, d time.Duration) int {
	t.Helper()

	if d == 0 {
		d = 9 * time.Minute
	}

	fmtNum := func(n int) string {
		s := fmt.Sprintf("%09d", n)
		return s[:3] + "_" + s[3:6] + "_" + s[6:]
	}

	var (
		max     = time.After(d)
		created int
	)
	for i := 0; i < n; i++ {
		select {
		case <-max:
			t.Logf("createFiles: stopped at %s files because it took longer than %s", fmtNum(created), d)
			return created
		default:
			path := join(dir, prefix+fmtNum(i))
			fp, err := os.Create(path)
			if err != nil {
				t.Errorf("create failed for %s: %s", fmtNum(i), err)
				continue
			}
			if err := fp.Close(); err != nil {
				t.Errorf("close failed for %s: %s", fmtNum(i), err)
			}
			if err := os.Remove(path); err != nil {
				t.Errorf("remove failed for %s: %s", fmtNum(i), err)
			}
			if i%10_000 == 0 {
				t.Logf("createFiles: %s", fmtNum(i))
			}
			created++
		}
	}
	return created
}

// mkdir
func mkdir(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("mkdir: path must have at least one element: %s", path)
	}
	err := os.Mkdir(join(path...), 0o0755)
	if err != nil {
		t.Fatalf("mkdir(%q): %s", join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// mkdir -p
func mkdirAll(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("mkdirAll: path must have at least one element: %s", path)
	}
	err := os.MkdirAll(join(path...), 0o0755)
	if err != nil {
		t.Fatalf("mkdirAll(%q): %s", join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// ln -s
func symlink(t *testing.T, target string, link ...string) {
	t.Helper()
	if len(link) < 1 {
		t.Fatalf("symlink: link must have at least one element: %s", link)
	}
	err := os.Symlink(target, join(link...))
	if err != nil {
		t.Fatalf("symlink(%q, %q): %s", target, join(link...), err)
	}
	if shouldWait(link...) {
		eventSeparator()
	}
}

// mkfifo
func mkfifo(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("mkfifo: path must have at least one element: %s", path)
	}
	err := internal.Mkfifo(join(path...), 0o644)
	if err != nil {
		t.Fatalf("mkfifo(%q): %s", join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// mknod
func mknod(t *testing.T, dev int, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("mknod: path must have at least one element: %s", path)
	}
	err := internal.Mknod(join(path...), 0o644, dev)
	if err != nil {
		t.Fatalf("mknod(%d, %q): %s", dev, join(path...), err)
	}
	if shouldWait(path...) {
		eventSeparator()
	}
}

// echoAppend and echoTrunc
func echoAppend(t *testing.T, data string, path ...string) { t.Helper(); echo(t, false, data, path...) }
func echoTrunc(t *testing.T, data string, path ...string)  { t.Helper(); echo(t, true, data, path...) }
func echo(t *testing.T, trunc bool, data string, path ...string) {
	n := "echoAppend"
	if trunc {
		n = "echoTrunc"
	}
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("%s: path must have at least one element: %s", n, path)
	}

	err := func() error {
		var (
			fp  *os.File
			err error
		)
		if trunc {
			fp, err = os.Create(join(path...))
		} else {
			fp, err = os.OpenFile(join(path...), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		}
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
		t.Fatalf("%s(%q): %s", n, join(path...), err)
	}
}

// touch
func touch(t *testing.T, path ...string) {
	t.Helper()
	if len(path) < 1 {
		t.Fatalf("touch: path must have at least one element: %s", path)
	}
	fp, err := os.Create(join(path...))
	if err != nil {
		t.Fatalf("touch(%q): %s", join(path...), err)
	}
	err = fp.Close()
	if err != nil {
		t.Fatalf("touch(%q): %s", join(path...), err)
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

	err := os.Rename(src, join(dst...))
	if err != nil {
		t.Fatalf("mv(%q, %q): %s", src, join(dst...), err)
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
	err := os.Remove(join(path...))
	if err != nil {
		t.Fatalf("rm(%q): %s", join(path...), err)
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
	err := os.RemoveAll(join(path...))
	if err != nil {
		t.Fatalf("rmAll(%q): %s", join(path...), err)
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
	err := os.Chmod(join(path...), mode)
	if err != nil {
		t.Fatalf("chmod(%q): %s", join(path...), err)
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
	w    *Watcher
	e    Events
	mu   sync.Mutex
	done chan struct{}
}

func newCollector(t *testing.T, add ...string) *eventCollector {
	return &eventCollector{
		w:    newWatcher(t, add...),
		done: make(chan struct{}),
		e:    make(Events, 0, 8),
	}
}

// stop collecting events and return what we've got.
func (w *eventCollector) stop(t *testing.T) Events {
	return w.stopWait(t, time.Second)
}

func (w *eventCollector) stopWait(t *testing.T, waitFor time.Duration) Events {
	waitForEvents()

	go func() {
		err := w.w.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	select {
	case <-time.After(waitFor):
		t.Fatalf("event stream was not closed after %s", waitFor)
	case <-w.done:
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.e
}

// Get all events we've found up to now and clear the event buffer.
func (w *eventCollector) events(t *testing.T) Events {
	w.mu.Lock()
	defer w.mu.Unlock()

	e := make(Events, len(w.e))
	copy(e, w.e)
	w.e = make(Events, 0, 16)
	return e
}

// Start collecting events.
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
				w.done <- struct{}{}
				return
			case e, ok := <-w.w.Events:
				if !ok {
					w.done <- struct{}{}
					return
				}
				w.mu.Lock()
				w.e = append(w.e, e)
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
//	CREATE        path
//	CREATE|WRITE  path
//
// Every event is one line, and any whitespace between the event and path are
// ignored. The path can optionally be surrounded in ". Anything after a "#" is
// ignored.
//
// Platform-specific tests can be added after GOOS:
//
//	# Tested if nothing else matches
//	CREATE   path
//
//	# Windows-specific test.
//	windows:
//	  WRITE    path
//
// You can specify multiple platforms with a comma (e.g. "windows, linux:").
// "kqueue" is a shortcut for all kqueue systems (BSD, macOS).
func newEvents(t *testing.T, s string) Events {
	t.Helper()

	var (
		lines  = strings.Split(s, "\n")
		groups = []string{""}
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
			groups = strings.Split(strings.TrimRight(line, ":"), ",")
			for i := range groups {
				groups[i] = strings.TrimSpace(groups[i])
			}
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			if strings.ToUpper(fields[0]) == "EMPTY" || strings.ToLower(fields[0]) == "no-events" {
				for _, g := range groups {
					events[g] = Events{}
				}
				continue
			}

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

		for _, g := range groups {
			events[g] = append(events[g], Event{Name: path, Op: op})
		}
	}

	if e, ok := events[runtime.GOOS]; ok {
		return e
	}
	switch runtime.GOOS {
	// kqueue shortcut
	case "freebsd", "netbsd", "openbsd", "dragonfly", "darwin":
		if e, ok := events["kqueue"]; ok {
			return e
		}
	// fen shortcut
	case "solaris", "illumos":
		if e, ok := events["fen"]; ok {
			return e
		}
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
		//t.Error("\n" + ztest.Diff(indent(haveSort), indent(wantSort)))
		t.Errorf("\nhave:\n%s\nwant:\n%s", indent(have), indent(want))
	}
}

func indent(s fmt.Stringer) string {
	return "\t" + strings.ReplaceAll(s.String(), "\n", "\n\t")
}

var join = filepath.Join

func isCI() bool {
	_, ok := os.LookupEnv("CI")
	return ok
}

func isKqueue() bool {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		return true
	}
	return false
}

func isSolaris() bool {
	switch runtime.GOOS {
	case "illumos", "solaris":
		return true
	}
	return false
}

func recurseOnly(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
		// Run test.
	default:
		t.Skip("recursion not yet supported on " + runtime.GOOS)
	}
}

func tmppath(tmp, s string) string {
	if len(s) == 0 {
		return ""
	}
	if !strings.HasPrefix(s, "./") {
		return filepath.Join(tmp, s)
	}
	// Needed for creating relative links. Support that only with explicit "./"
	// – otherwise too easy to forget leading "/" and create files outside of
	// the tmp dir.
	return s
}

type command struct {
	line int
	cmd  string
	args []string
}

func parseScript(t *testing.T, in string) {
	var (
		lines = strings.Split(in, "\n")
		cmds  = make([]command, 0, 8)
		readW bool
		want  string
		tmp   = t.TempDir()
	)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if i := strings.IndexByte(line, '#'); i > -1 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "Output:" {
			readW = true
			continue
		}
		if readW {
			want += line + "\n"
			continue
		}

		cmd := command{line: i + 1, args: make([]string, 0, 4)}
		var (
			q   bool
			cur = make([]rune, 0, 16)
			app = func() {
				if len(cur) == 0 {
					return
				}
				if cmd.cmd == "" {
					cmd.cmd = string(cur)
				} else {
					cmd.args = append(cmd.args, string(cur))
				}
				cur = cur[:0]
			}
		)
		for _, c := range line {
			switch c {
			case ' ', '\t':
				if q {
					cur = append(cur, c)
				} else {
					app()
				}
			case '"', '\'': // '
				q = !q
			default:
				cur = append(cur, c)
			}
		}
		app()
		cmds = append(cmds, cmd)
	}

	var (
		do      = make([]func(), 0, len(cmds))
		w       = newCollector(t)
		mustArg = func(c command, n int) {
			if len(c.args) != n {
				t.Fatalf("line %d: %q requires exactly %d argument (have %d: %q)",
					c.line, c.cmd, n, len(c.args), c.args)
			}
		}
	)
	for _, c := range cmds {
		c := c
		//fmt.Printf("line %d: %q  %q\n", c.line, c.cmd, c.args)
		switch c.cmd {
		case "skip", "require":
			mustArg(c, 1)
			switch c.args[0] {
			case "always":
				t.Skip()
			case "symlink":
				if !internal.HasPrivilegesForSymlink() {
					t.Skipf("%s symlink: admin permissions required on Windows", c.cmd)
				}
			case "mkfifo":
				if runtime.GOOS == "windows" {
					t.Skip("No named pipes on Windows")
				}
			case "recurse":
				recurseOnly(t)
			case "mknod":
				if runtime.GOOS == "windows" {
					t.Skip("No device nodes on Windows")
				}
				if isKqueue() {
					// Don't want to use os/user to check uid, since that pulls
					// in cgo by default and stuff that uses fsnotify won't be
					// statically linked by default.
					t.Skip("needs root on BSD")
				}
				if isSolaris() {
					t.Skip(`"mknod fails with "not owner"`)
				}
			case "windows":
				if runtime.GOOS == "windows" {
					t.Skip("Skipping on Windows")
				}
			case "netbsd":
				if runtime.GOOS == "netbsd" {
					t.Skip("Skipping on NetBSD")
				}
			default:
				t.Fatalf("line %d: unknown %s reason: %q", c.line, c.cmd, c.args[0])
			}
		case "watch":
			mustArg(c, 1)
			do = append(do, func() { addWatch(t, w.w, tmppath(tmp, c.args[0])) })
		case "unwatch":
			mustArg(c, 1)
			do = append(do, func() { rmWatch(t, w.w, tmppath(tmp, c.args[0])) })
		case "watchlist":
			mustArg(c, 1)
			n, err := strconv.ParseInt(c.args[0], 10, 0)
			if err != nil {
				t.Fatalf("line %d: %s", c.line, err)
			}
			do = append(do, func() {
				wl := w.w.WatchList()
				if l := int64(len(wl)); l != n {
					t.Errorf("line %d: watchlist has %d entries, not %d\n%q", c.line, l, n, wl)
				}
			})
		case "touch":
			mustArg(c, 1)
			do = append(do, func() { touch(t, tmppath(tmp, c.args[0])) })
		case "mkdir":
			recur := false
			if len(c.args) == 2 && c.args[0] == "-p" {
				recur, c.args = true, c.args[1:]
			}
			mustArg(c, 1)
			if recur {
				do = append(do, func() { mkdirAll(t, tmppath(tmp, c.args[0])) })
			} else {
				do = append(do, func() { mkdir(t, tmppath(tmp, c.args[0])) })
			}
		case "ln":
			mustArg(c, 3)
			if c.args[0] != "-s" {
				t.Fatalf("line %d: only ln -s is supported", c.line)
			}
			do = append(do, func() { symlink(t, tmppath(tmp, c.args[1]), tmppath(tmp, c.args[2])) })
		case "mkfifo":
			mustArg(c, 1)
			do = append(do, func() { mkfifo(t, tmppath(tmp, c.args[0])) })
		case "mknod":
			mustArg(c, 2)
			n, err := strconv.ParseInt(c.args[0], 10, 0)
			if err != nil {
				t.Fatalf("line %d: %s", c.line, err)
			}
			do = append(do, func() { mknod(t, int(n), tmppath(tmp, c.args[1])) })
		case "mv":
			mustArg(c, 2)
			do = append(do, func() { mv(t, tmppath(tmp, c.args[0]), tmppath(tmp, c.args[1])) })
		case "rm":
			recur := false
			if len(c.args) == 2 && c.args[0] == "-r" {
				recur, c.args = true, c.args[1:]
			}
			mustArg(c, 1)
			if recur {
				do = append(do, func() { rmAll(t, tmppath(tmp, c.args[0])) })
			} else {
				do = append(do, func() { rm(t, tmppath(tmp, c.args[0])) })
			}
		case "chmod":
			mustArg(c, 2)
			n, err := strconv.ParseUint(c.args[0], 8, 32)
			if err != nil {
				t.Fatalf("line %d: %s", c.line, err)
			}
			do = append(do, func() { chmod(t, fs.FileMode(n), tmppath(tmp, c.args[1])) })
		case "echo":
			if len(c.args) < 2 || len(c.args) > 3 {
				t.Fatalf("line %d: %q requires 2 or 3 arguments (have %d: %q)",
					c.line, c.cmd, len(c.args), c.args)
			}

			var data, op, dst string
			if len(c.args) == 2 { // echo foo >dst
				data, op, dst = c.args[0], c.args[1][:1], c.args[1][1:]
				if strings.HasPrefix(dst, ">") {
					op, dst = op+dst[:1], dst[1:]
				}
			} else { // echo foo > dst
				data, op, dst = c.args[0], c.args[1], c.args[2]
			}

			switch op {
			case ">":
				do = append(do, func() { echoTrunc(t, data, tmppath(tmp, dst)) })
			case ">>":
				do = append(do, func() { echoAppend(t, data, tmppath(tmp, dst)) })
			default:
				t.Fatalf("line %d: echo requires > (truncate) or >> (append): echo data >file", c.line)
			}
		case "sleep":
			mustArg(c, 1)
			n, err := strconv.ParseInt(strings.TrimRight(c.args[0], "ms"), 10, 0)
			if err != nil {
				t.Fatalf("line %d: %s", c.line, err)
			}
			do = append(do, func() { time.Sleep(time.Duration(n) * time.Millisecond) })
		default:
			t.Errorf("line %d: unknown command %q", c.line, c.cmd)
		}
	}

	w.collect(t)
	for _, d := range do {
		d()
	}
	ev := w.stop(t)
	cmpEvents(t, tmp, ev, newEvents(t, want))
}
