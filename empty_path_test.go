package fsnotify

import "testing"

func TestEmptyPathRejected(t *testing.T) {
	w, err := NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Add(""); err != ErrEmptyPath {
		t.Fatalf("Add(\"\"): got %v want %v", err, ErrEmptyPath)
	}
	if err := w.AddWith(""); err != ErrEmptyPath {
		t.Fatalf("AddWith(\"\"): got %v want %v", err, ErrEmptyPath)
	}
	if err := w.Remove(""); err != ErrEmptyPath {
		t.Fatalf("Remove(\"\"): got %v want %v", err, ErrEmptyPath)
	}
}
