# File system notifications for Go

[![Coverage](http://gocover.io/_badge/github.com/fsnotify/fsnotify)](http://gocover.io/github.com/fsnotify/fsnotify) [![GoDoc](https://godoc.org/github.com/fsnotify/fsnotify?status.png)](http://godoc.org/github.com/fsnotify/fsnotify)

Cross platform: Windows, Linux, BSD and OS X.

Please see [the documentation](http://godoc.org/github.com/fsnotify/fsnotify) for usage. The [Wiki](https://github.com/fsnotify/fsnotify/wiki) contains an FAQ and other information.

## API stability

The fsnotify API has changed from what exists at `github.com/howeyc/fsnotify` ([GoDoc](http://godoc.org/github.com/howeyc/fsnotify)).

Further changes are expected. You may use [gopkg.in](https://gopkg.in/fsnotify/fsnotify.v0) to lock to the current API: 

```go
import "gopkg.in/fsnotify/fsnotify.v0"
```

A new major revision will be tagged for any future API changes.

## Contributing

* Send questions to [golang-dev@googlegroups.com](mailto:golang-dev@googlegroups.com). 
* Request features and report bugs using the [GitHub Issue Tracker](https://github.com/fsnotify/fsnotify/issues).

A future version of Go will have [fsnotify in the standard library](https://code.google.com/p/go/issues/detail?id=4068), therefore fsnotify carries the same [LICENSE](https://github.com/fsnotify/fsnotify/blob/master/LICENSE) as Go. Contributors retain their copyright, so we need you to fill out a short form before we can accept your contribution: [Google Individual Contributor License Agreement](https://developers.google.com/open-source/cla/individual).

Please read [CONTRIBUTING](https://github.com/fsnotify/fsnotify/blob/master/CONTRIBUTING.md) before opening a pull request.

## Example

See [example_test.go](https://github.com/fsnotify/fsnotify/blob/master/example_test.go).
