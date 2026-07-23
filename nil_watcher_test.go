package fsnotify

import "testing"

func TestNilWatcher(t *testing.T) {
	var w *Watcher
	if err := w.Add("x"); err == nil {
		t.Fatal("Add want error")
	}
	if err := w.Remove("x"); err == nil {
		t.Fatal("Remove want error")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if w.WatchList() != nil {
		t.Fatal("WatchList want nil")
	}
}
