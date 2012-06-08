# File system notifications for Go

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

[GoDoc](http://go.pkgdoc.org/github.com/howeyc/fsnotify)
