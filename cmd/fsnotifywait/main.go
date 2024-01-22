// Command fsnotifywait mimics the behavior of the Linux inotifywait command.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var usage = `
fsnotifywait is cli tool that mimics the behavior of the Linux inotifywait command in a simplified way.
(very very simplified)

https://github.com/weka/fsnotify

Usage:

    fsnotifywait [path]  Watch the path for changes and exit when an event is received.

`[1:]

func exit(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, filepath.Base(os.Args[0])+": "+format+"\n", a...)
	fmt.Print("\n" + usage)
	os.Exit(1)
}

func help() {
	fmt.Print(usage)
	os.Exit(0)
}

// Print line prefixed with the time (a bit shorter than log.Print; we don't
// really need the date and ms is useful here).
func printTime(s string, args ...interface{}) {
	fmt.Printf(time.Now().Format("15:04:05.0000")+" "+s+"\n", args...)
}

func main() {
	if len(os.Args) == 1 {
		help()
	}
	// Always show help if -h[elp] appears anywhere before we do anything else.
	for _, f := range os.Args[1:] {
		switch f {
		case "help", "-h", "-help", "--help":
			help()
		}
	}

	watchFolder := os.Args[1]
	fmt.Println("watchFolder:", watchFolder)

	if _, err := os.Stat(watchFolder); os.IsNotExist(err) {
		exit("Folder does not exist")
	}

	watch(watchFolder)
}
