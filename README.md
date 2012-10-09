# File system notifications for Go

[GoDoc](http://go.pkgdoc.org/github.com/howeyc/fsnotify)

Cross platform, works on:
* Windows
* Linux
* BSD
* OSX

Example:
```go
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        log.Fatal(err)
    }

    // Process events
    go func() {
        for {
            select {
            case ev := <-watcher.Event:
                log.Println("event:", ev)
            case err := <-watcher.Error:
                log.Println("error:", err)
            }
        }
    }()

    err = watcher.Watch("/tmp")
    if err != nil {
        log.Fatal(err)
    }

    /* ... do stuff ... */
    watcher.Close()
```

For each event:
* Name
* IsCreate()
* IsDelete()
* IsModify()
* IsRename()

Notes:
* When a file is renamed to another directory is it still being watched?
    * No (it shouldn't be, unless you are watching where it was moved to).
* When I watch a directory, are all subdirectories watched as well?
    * No, you must add watches for any directory you want to watch.
* Do I have to watch the Error and Event channels in a separate goroutine?
    * As of now, yes. Looking into making this single-thread friendly.

[![Build Status](https://secure.travis-ci.org/howeyc/fsnotify.png?branch=master)](http://travis-ci.org/howeyc/fsnotify)

