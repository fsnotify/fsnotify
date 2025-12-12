# AI Coding Agent Instructions for fsnotify

## Overview
`fsnotify` is a Go library for cross-platform filesystem notifications. It supports platforms like Linux, macOS, Windows, BSD, and illumos. The library uses platform-specific backends such as `inotify` (Linux), `kqueue` (BSD/macOS), and `ReadDirectoryChangesW` (Windows) to monitor filesystem events.

## Key Components
- **Core Library**: The main implementation resides in `fsnotify.go` and platform-specific files like `system_darwin.go` and `backend_kqueue.go`.
- **Command-line Examples**: Located in `cmd/fsnotify/`, these demonstrate usage patterns.
- **Tests**: Unit tests are in `*_test.go` files, while integration tests use scripts in `testdata/`.
- **Platform-Specific Code**: Files in `internal/` and `backend_*` handle OS-specific implementations.

## Developer Workflows
### Building
- Use `go build` to compile the library.

### Testing
- Run all tests with:
  ```bash
  go test ./...
  ```
- Use the `-short` flag to skip stress tests:
  ```bash
  go test -short ./...
  ```
- Integration tests are defined in `testdata/`. Run specific tests with:
  ```bash
  go test -run TestScript/[path]
  ```

### Debugging
- Enable debug logs by setting the `FSNOTIFY_DEBUG` environment variable:
  ```bash
  export FSNOTIFY_DEBUG=1
  ```

## Project-Specific Conventions
- **Event Handling**: Always watch directories instead of individual files to avoid issues with atomic file updates.
- **Error Handling**: Monitor both `watcher.Events` and `watcher.Errors` channels in a `select` loop.
- **Platform Limits**:
  - Linux: Adjust `fs.inotify.max_user_watches` and `fs.inotify.max_user_instances` for large-scale monitoring.
  - macOS/BSD: Increase `kern.maxfiles` and `kern.maxfilesperproc` for higher file descriptor limits.

## External Dependencies
- `golang.org/x/sys`: Provides low-level OS-specific functionality.

## Examples
- Basic usage example:
  ```go
  watcher, err := fsnotify.NewWatcher()
  if err != nil {
      log.Fatal(err)
  }
  defer watcher.Close()

  err = watcher.Add("/tmp")
  if err != nil {
      log.Fatal(err)
  }

  for {
      select {
      case event := <-watcher.Events:
          log.Println("event:", event)
      case err := <-watcher.Errors:
          log.Println("error:", err)
      }
  }
  ```
- More examples are in `cmd/fsnotify/`.

## Notes
- Recursive watching is not supported yet (see [#18](https://github.com/fsnotify/fsnotify/issues/18)).
- Polling-based watching is planned but not implemented (see [#9](https://github.com/fsnotify/fsnotify/issues/9)).

## Contributing to fsnotify

### Getting Started
1. **Fork the Repository**: Ensure you have forked the `fsnotify` repository on GitHub.
2. **Clone Your Fork**: Clone your fork locally and set up the upstream remote:
   ```bash
   git clone https://github.com/<your-username>/fsnotify.git
   cd fsnotify
   git remote add upstream https://github.com/fsnotify/fsnotify.git
   ```
3. **Sync with Upstream**: Regularly sync your fork with the upstream repository to stay updated:
   ```bash
   git fetch upstream
   git merge upstream/main
   ```

### Working on Issues
- **Target Issue**: You are focusing on [#717](https://github.com/fsnotify/fsnotify/issues/717), which may be related to [#11](https://github.com/fsnotify/fsnotify/issues/11).
- **Reproduction Code**: Issue #717 provides a convenient reproduction script. Use this to verify the problem and test your fixes.
- **Understanding the Problem**: The issue involves macOS-specific behavior with `kqueue`. Familiarize yourself with `backend_kqueue.go` and `system_darwin.go`.

### Development Workflow
1. **Create a Branch**: Always create a new branch for your work:
   ```bash
   git checkout -b fix-issue-717
   ```
2. **Write Tests**: Add tests in `testdata/` to validate your changes. Use the script-based test format described in [`CONTRIBUTING.md`](../CONTRIBUTING.md).
3. **Run Tests**: Ensure all tests pass locally before pushing:
   ```bash
   go test ./...
   ```
4. **Debugging**: Use the `FSNOTIFY_DEBUG` environment variable to enable verbose logging during development.
5. **Commit Changes**: Write clear and concise commit messages:
   ```bash
   git commit -m "Fix issue #717: Handle kqueue edge case on macOS"
   ```
6. **Push and Create PR**: Push your branch and create a pull request against the `fsnotify` repository:
   ```bash
   git push origin fix-issue-717
   ```

### Tips for Success
- **Collaborate**: Comment on the GitHub issue to share your progress and ask for feedback.
- **Follow Guidelines**: Ensure your code adheres to the project's conventions and is compatible across platforms.
- **Review Existing PRs**: Look at other pull requests for examples of good contributions.

### Resources
- [`CONTRIBUTING.md`](../CONTRIBUTING.md): Detailed contribution guidelines.
- [`README.md`](../README.md): Overview and usage examples.
- [Issue #717](https://github.com/fsnotify/fsnotify/issues/717): Your target issue.
- [Issue #11](https://github.com/fsnotify/fsnotify/issues/11): Related long-standing issue.

## Session Findings

- **Issue Reproduction**: The issue described in [#717](https://github.com/fsnotify/fsnotify/issues/717) has been successfully reproduced by creating a new test file, `issue717_test.go`, which consistently fails on macOS.

- **Problem Analysis**: The core of the issue is a race condition specific to the `kqueue` backend on macOS. When a file is deleted and then quickly recreated with new content, a `CREATE` event is received, but the subsequent `WRITE` event is often missed. This leads to a timeout in the test, as the file is read before its new content is available.

- **Investigation of `backend_kqueue.go`**:
  - The investigation has been focused on `backend_kqueue.go`, which is the correct location for the `kqueue` implementation.
  - Several attempts to fix the issue have been unsuccessful:
    - Adding a `sleep` to the `dirChange` function to delay the directory scan did not resolve the race condition.
    - Checking the file's modification time to manually trigger a `WRITE` event was also not successful.
    - Forcing a `WRITE` event to be sent after a `DELETE` event did not fix the issue.

- **Current Status**: The codebase is back in its original state, but with the addition of the `issue717_test.go` file, which serves as a reliable metric for a successful fix when ran on a macOS system.
