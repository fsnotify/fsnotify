package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

func fatal(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", filepath.Base(os.Args[0]), err)
	os.Exit(1)
}

func line(s string, args ...interface{}) {
	fmt.Printf(time.Now().Format("15:16:05.0000")+" "+s+"\n", args...)
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("must specify at least one path to watch"))
	}

	w, err := fsnotify.NewWatcher()
	fatal(err)
	defer w.Close()

	go func() {
		i := 0
		for {
			select {
			case e, ok := <-w.Events:
				if !ok {
					return
				}

				i++
				m := ""
				if e.Op&fsnotify.Write == fsnotify.Write {
					m = "(modified)"
				}
				line("%3d %-10s %-10s %q", i, e.Op, m, e.Name)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				line("ERROR: %s", err)
			}
		}
	}()

	for _, p := range os.Args[1:] {
		err = w.Add(p)
		if err != nil {
			fatal(fmt.Errorf("%q: %w", p, err))
		}
	}

	line("watching; press ^C to exit")
	<-make(chan struct{})
}
