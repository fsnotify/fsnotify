package fsnotify

import (
	"io/ioutil"
	"log"
	"os"
	"sync"
	"testing"
	//"time"

	"github.com/stretchr/testify/assert"
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
			notifications += 1
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
		index += 1
	}
}
func TestWatcherClose(t *testing.T) {
	dir, err := ioutil.TempDir("./", "example")
	assert.NoError(t, err)
	var wg sync.WaitGroup
	watcher, err := NewWatcher()
	assert.NoError(t, err)
	err = watcher.Add(dir)
	assert.NoError(t, err)
	_, err = ioutil.TempFile(dir, "example")
	assert.NoError(t, err)
	ioutil.TempFile(dir, "example")
	wg.Add(1)
	go writeFiles(dir)
	go consumeEvent(t, watcher, &wg)
	wg.Wait()
	os.RemoveAll(dir)
}
