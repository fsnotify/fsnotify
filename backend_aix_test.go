//go:build aix
// +build aix

package fsnotify

import (
		"fmt"
		"os"
		"path/filepath"
		"testing"
		"time"
)

func TestWatcher_StepByStepEvents(t *testing.T) {
		tmpDir := t.TempDir()
		pollInterval := 500 * time.Millisecond

		watcher := newWatcher(t, tmpDir)
		defer watcher.Close()

		testFile := filepath.Join(tmpDir, "stepfile.txt")

		// Let watcher take initial snapshot
		time.Sleep(pollInterval)

		// Step 1: Create
		f, err := os.Create(testFile)
		if err != nil {
				t.Fatalf("Failed to create file: %v", err)
		}
		f.Close()
		waitForOp(t, watcher, Create)

		// Step 2: Write
		time.Sleep(1 * time.Second) // ensure modTime changes
		err = os.WriteFile(testFile, []byte("modified"), 0644)
		if err != nil {
				t.Fatalf("Failed to write to file: %v", err)
		}
		waitForOp(t, watcher, Write)

    	// Step 3: Rename
		newFile := filepath.Join(tmpDir, "renamed.txt")
		err = os.Rename(testFile, newFile)
		if err != nil {
				t.Fatalf("Failed to rename file: %v", err)
		}
		waitForOp(t, watcher, Rename)

		// Step 4: Delete the renamed file
		err = os.Remove(newFile)
		if err != nil {
				t.Fatalf("Failed to delete file: %v", err)
		}
		waitForOp(t, watcher, Remove)

		fmt.Println("All events (create, write, delete) detected successfully.")
}

func waitForOp(t *testing.T, w *Watcher, expectedOp Op) {
		timeout := time.After(3 * time.Second)
		for {
				select {
							case evt := <-w.Events:
									if evt.Op == expectedOp {
											fmt.Printf("Detected %s on %s\n", evt.Op, evt.Name)
											return
									} else {
											fmt.Printf("Got unexpected op: %s (expected %s)\n", evt.Op, expectedOp)
									}
							case <-timeout:
									t.Fatalf("Timeout waiting for %s event", expectedOp)
				}
		}
}
