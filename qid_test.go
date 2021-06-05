// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build plan9

package fsnotify

import (
	"os"
	"testing"
	"time"
)

func TestWatcher(t *testing.T) {
	f, err := os.CreateTemp("", "fsnotify-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	w, _ := NewWatcher()
	w.Add(f.Name())
	//Dont modify before the watch goroutine can get a version of it
	time.Sleep(2 * time.Second)
	f.Write([]byte("Hello"))
	e := <-w.Events
	if e.Op != Write {
		t.Fatalf("expected Write op, got %v", e)
	}
	os.Chmod(f.Name(), 0777)
	e = <-w.Events
	if e.Op != Chmod {
		t.Fatalf("execpted Chmod op, got %v", e)
	}
	os.Remove(f.Name())
	e = <-w.Events
	if e.Op != Remove {
		t.Fatalf("expected Remove op, got %v", e)
	}
	os.Create(f.Name())
	e = <-w.Events
	if e.Op != Create {
		t.Fatalf("expected Create op, got %v", e)
	}
}
