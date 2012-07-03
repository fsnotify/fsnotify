package fsnotify_test

import (
	"github.com/howeyc/fsnotify"
	"log"
)

func ExampleNewWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			select {
			case ev := <-watcher.Event:
				log.Println("event:", ev)
			case err := <-watcher.Error:
				log.Println("error:", err)
			}
		}
	}()

	err = watcher.Watch("/tmp/foo")
	if err != nil {
		log.Fatal(err)
	}
}
