package fsnotify

import (
	"io/ioutil"
	"log"
	"os"
	"sync"
	"testing"
)

func consumeEvent(t *testing.T, watcher *Watcher, wg *sync.WaitGroup) {
	defer func() {
		//time.Sleep(time.Second)
		watcher.Close()
		wg.Done()
	}()
	log.Println("Came here")
	notifications := 0
	maxNotifications := 1
	for {
		if notifications >= maxNotifications {
			log.Println("Reached max notifications")
			return
		}
		select {
		case event := <-watcher.Events:
			log.Println("event:", event)
			notifications++
			continue
		case err := <-watcher.Errors:
			log.Println("error:", err)
		}
	}
}

func writeFiles(dir string) {
	index := 0
	for {
		ioutil.TempFile(dir, "example")
		ioutil.TempFile(dir, "example")
		if index > 10 {
			return
		}
		index++
	}
}
func TestWatcherClose(t *testing.T) {
	dir, err := ioutil.TempDir("./", "example")
	if err != nil {
		panic(err)
	}
	var wg sync.WaitGroup
	watcher, err := NewWatcher()
	if err != nil {
		panic(err)
	}
	err = watcher.Add(dir)
	if err != nil {
		panic(err)
	}
	_, err = ioutil.TempFile(dir, "example")
	if err != nil {
		panic(err)
	}
	ioutil.TempFile(dir, "example")
	wg.Add(1)
	go writeFiles(dir)
	go consumeEvent(t, watcher, &wg)
	wg.Wait()
	os.RemoveAll(dir)
}
