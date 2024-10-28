package main

import (
	"math"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Depending on the system, a single "write" can generate many Write events; for
// example compiling a large Go program can generate hundreds of Write events on
// the binary.
//
// Some systems support delivering "close" events, so we can check for that.
//
// Other platforms don't support this. The general strategy to deal with this is
// to wait a short time for more write events, resetting the wait period for
// every new event.
func finishWrite(paths ...string) {
	if len(paths) < 1 {
		exit("must specify at least one path to watch")
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		exit("creating a new watcher: %s", err)
	}
	defer w.Close()

	// Check if CloseWrite is supported.
	var (
		op fsnotify.Op
		cw = w.Supports(fsnotify.UnportableCloseWrite)
	)
	if cw {
		op |= fsnotify.UnportableCloseWrite
	} else {
		op |= fsnotify.Create | fsnotify.Write
	}

	go finishWriteLoop(w, cw)

	for _, p := range paths {
		err := w.AddWith(p, fsnotify.WithOps(op))
		if err != nil {
			exit("%q: %s", p, err)
		}
	}

	printTime("ready; press ^C to exit")
	<-make(chan struct{})
}

func finishWriteLoop(w *fsnotify.Watcher, cw bool) {
	var (
		// Wait 100ms for new events; each new event resets the timer.
		waitFor = 100 * time.Millisecond

		// Keep track of the timers, as path → timer.
		timers = make(map[string]*time.Timer)
		mu     sync.Mutex
	)
	for {
		select {
		// Read from Errors.
		case err, ok := <-w.Errors:
			if !ok { // Channel was closed (i.e. Watcher.Close() was called).
				return
			}
			panic(err)

		// Read from Events.
		case e, ok := <-w.Events:
			if !ok { // Channel was closed (i.e. Watcher.Close() was called).
				return
			}

			// CloseWrite is supported: easy case.
			if cw {
				if e.Has(fsnotify.UnportableCloseWrite) {
					printTime(e.String())
				}
				continue
			}

			// CloseWrite is not supported, wait until we stop receiving events.

			// Get timer.
			mu.Lock()
			t, ok := timers[e.Name]
			mu.Unlock()

			// No timer yet, so create one.
			if !ok {
				t = time.AfterFunc(math.MaxInt64, func() {
					printTime(e.String())
					mu.Lock()
					delete(timers, e.Name)
					mu.Unlock()
				})
				t.Stop()

				mu.Lock()
				timers[e.Name] = t
				mu.Unlock()
			}

			// Reset the timer for this path, so it will start from 100ms again.
			t.Reset(waitFor)
		}
	}
}
