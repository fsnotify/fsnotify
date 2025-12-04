//go:build go1.25

package fsnotify

import (
	"testing"
	"testing/synctest"
)

func TestSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, err := NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		err = w.Close()
		if err != nil {
			t.Fatal(err)
		}
	})
}
