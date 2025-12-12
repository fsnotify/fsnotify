// Copyright 2024 The fsnotify Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !plan9

package fsnotify

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

var runOnLinux = flag.Bool("linux", false, "Run test on Linux via Docker to validate test logic")

// TestKqueueDeleteRecreateRace tests a race condition in the kqueue backend (issue #717).
//
// Bug: When a file is deleted and quickly recreated with new content, kqueue may
// miss the WRITE event, causing the file to be read before new content is available.
//
// Test behavior:
//   - Watches a directory containing a file with "initial" content
//   - Deletes and recreates the file with "recreated" content
//   - Expects the watcher to eventually see the new content
//
// Expected results:
//   - Linux (inotify): PASS - confirms test logic is correct
//   - macOS/BSD (kqueue): FAIL - demonstrates the race condition
//
// Run locally: go test -run TestKqueueDeleteRecreateRace -v
// Run on Linux (via Docker): go test -run TestKqueueDeleteRecreateRace -v -args -linux
func TestKqueueDeleteRecreateRace(t *testing.T) {
	// Number of iterations per run - enough to trigger the race on kqueue
	const iterations = 100
	// Number of test runs - needs to be high enough to reliably trigger the race
	const runs = 50

	if *runOnLinux {
		runLinuxValidation(t, iterations, runs)
		return
	}

	runKqueueRaceTest(t, iterations, runs)
}

// runLinuxValidation runs the test inside a Linux Docker container to validate
// that the test logic is correct (should pass on inotify).
func runLinuxValidation(t *testing.T, iterations, runs int) {
	t.Helper()

	// Check if Docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available - skipping Linux validation")
	}

	// Check if Docker daemon is running
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		t.Skip("Docker daemon not running - skipping Linux validation")
	}

	t.Log("Running Linux validation via Docker...")
	t.Logf("Parameters: %d runs × %d iterations = %d total", runs, iterations, runs*iterations)

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Run the test in Docker - no -linux flag needed inside container
	// The test runs locally on Linux (inotify) which should always pass
	dockerCmd := exec.Command("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/app", cwd),
		"-w", "/app",
		"golang:1.22-alpine",
		"go", "test",
		"-run", "TestKqueueDeleteRecreateRace",
		"-v",
	)

	var stdout, stderr bytes.Buffer
	dockerCmd.Stdout = &stdout
	dockerCmd.Stderr = &stderr

	err = dockerCmd.Run()
	output := stdout.String() + stderr.String()

	// Log the output
	for _, line := range strings.Split(output, "\n") {
		if line != "" {
			t.Log("  [Linux]", line)
		}
	}

	if err != nil {
		t.Errorf("LINUX VALIDATION FAILED: Test logic may be broken!\n"+
			"The test should pass on Linux (inotify). If it fails, "+
			"the test itself has a bug, not fsnotify.\nError: %v", err)
	} else {
		t.Log("LINUX VALIDATION PASSED: Test logic is correct.")
	}
}

// runKqueueRaceTest runs the kqueue race condition test iterations.
func runKqueueRaceTest(t *testing.T, iterations, runs int) {
	t.Helper()

	var (
		totalPassed  int
		totalFailed  int
		firstFailAt  = -1
		firstFailRun = -1
	)

	for run := 0; run < runs; run++ {
		tmp := t.TempDir()
		dir := join(tmp, "cfgDir")
		mkdirAll(t, dir)
		filename := join(dir, "config.yaml")

		for i := 0; i < iterations; i++ {
			// Create initial file directly without eventSeparator delays
			if err := os.WriteFile(filename, []byte("initial"), 0o644); err != nil {
				t.Fatalf("create initial file: %v", err)
			}

			if runRaceIteration(t, dir, filename) {
				totalPassed++
			} else {
				totalFailed++
				if firstFailAt < 0 {
					firstFailAt = i
					firstFailRun = run
				}
				// On non-kqueue systems, fail immediately - the test logic should work
				if !isKqueue() {
					t.Fatalf("UNEXPECTED FAILURE on %s (run %d, iteration %d): "+
						"This test should pass on inotify/ReadDirectoryChangesW backends. "+
						"If this fails, the test logic may be broken.", runtime.GOOS, run, i)
				}
				// On kqueue, we found the bug - stop this run
				break
			}
		}

		// If we found a failure on kqueue, stop all runs
		if isKqueue() && totalFailed > 0 {
			break
		}
	}

	totalIterations := totalPassed + totalFailed
	t.Logf("Results on %s (%s backend): %d passed, %d failed out of %d iterations",
		runtime.GOOS, backendName(), totalPassed, totalFailed, totalIterations)

	if isKqueue() {
		if totalFailed > 0 {
			// This is expected on kqueue - the bug we're testing for
			t.Errorf("CONFIRMED: kqueue delete+recreate race reproduced on %s (run %d, iteration %d). "+
				"WRITE event missed after CREATE.",
				runtime.GOOS, firstFailRun, firstFailAt)
		} else {
			t.Logf("Race condition NOT reproduced after %d runs × %d iterations. "+
				"The bug may be fixed.", runs, iterations)
		}
	}
}

// backendName returns a human-readable name for the current platform's backend.
func backendName() string {
	switch runtime.GOOS {
	case "linux", "android":
		return "inotify"
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "kqueue"
	case "windows":
		return "ReadDirectoryChangesW"
	case "solaris", "illumos":
		return "FEN"
	default:
		return "unknown"
	}
}

// runRaceIteration runs a single iteration of the race condition test.
// Returns true if the iteration passed (got expected content), false if it timed out.
func runRaceIteration(t *testing.T, dir, filename string) bool {
	t.Helper()

	w := newWatcher(t, dir)

	gotContent := make(chan string, 10)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
				// Ignore errors - we may close the watcher while it's running
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}

				// Read file on any event for the target file (matching original test)
				if ev.Name != filename {
					continue
				}

				content, err := os.ReadFile(filename)
				if err != nil {
					// File might not exist on REMOVE, that's expected
					continue
				}

				select {
				case gotContent <- string(content):
				default:
				}
			}
		}
	}()

	// Delete and recreate the file quickly
	if err := os.Remove(filename); err != nil {
		w.Close()
		<-done
		t.Fatalf("remove %q: %v", filename, err)
	}
	if err := os.WriteFile(filename, []byte("recreated"), 0o644); err != nil {
		w.Close()
		<-done
		t.Fatalf("write %q: %v", filename, err)
	}

	// Wait for the expected content (3 second timeout matching original test)
	timeout := time.After(3 * time.Second)
	for {
		select {
		case content := <-gotContent:
			if content == "recreated" {
				w.Close()
				<-done
				return true
			}
		case <-timeout:
			w.Close()
			<-done
			return false
		}
	}
}
