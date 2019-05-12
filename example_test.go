// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !plan9

package fsnotify_test

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// If you want to execute example code, append prefix 'Test'.
// ex) func TestExampleNewWatcher(t *testing.T)
func exampleNewWatcher(t *testing.T) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Error(err)
	}
	defer watcher.Close()

	err = watcher.Add(".")
	if err != nil {
		t.Error(err)
	}

	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
MAIN:
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok { // when close channel ex) close(watcher.Events)
				break MAIN
			}
			log.Println("event:", event)
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println("modified file:", event.Name)
			}

		case err, ok := <-watcher.Errors:
			if !ok { // when close channel ex) close(watcher.Errors)
				break MAIN
			}
			log.Println("error:", err)

		case signal := <-sigs:
			if signal == os.Interrupt {
				break MAIN
				// close(watcher.Events) or close(watcher.Errors)
			}
		}
	}
}
