# Changelog

## r2013-10-19

* [Fix] kqueue: remove file watches when parent directory is removed #71 (reported by @mdwhatcott)
* [Fix] kqueue: race between Close and readEvents #70 (reported by @bernerdschaefer)
* [Doc] specify OS-specific limits in README (thanks @debrando)

## r2013-09-08

* [Doc] update package path in example code #63 (thanks @paulhammond)
* [Doc] Contributing document (thanks @nathany)
* [Doc] Cross-platform testing with Vagrant  #59 (thanks @nathany)
* [Doc] GoCI badge in README (Linux only) #60

## r2013-06-17

* [Fix] Windows: handle `ERROR_MORE_DATA` on Windows #49 (thanks @jbowtie)

## r2013-06-03

* [Fix] inotify: ignore event changes
* lower case error messages
* [API] Make syscall flags internal
* [Fix] race in symlink test #45 (reported by @srid)
* [Fix] tests on Windows

## r2013-05-23

* kqueue: Use EVT_ONLY flag on Darwin
* [Doc] Update README with full example

## r2013-05-09

* [Fix] inotify: allow monitoring of "broken" symlinks (thanks @tsg)

## r2013-04-07

* [Fix] kqueue: watch all file events #40 (thanks @ChrisBuchholz)

## r2013-03-13

* [Fix] inoitfy/kqueue memory leak #36 (reported by @nbkolchin)
* [Fix] kqueue: use fsnFlags for watching a directory #33 (reported by @nbkolchin)

## r2013-02-07

* [Doc] add Authors
* [Fix] fix data races for map access #29 (thanks @fsouza)

## r2013-01-09

* [Fix] Windows path separators

## r2012-12-17

* [Doc] BSD License

## r2012-11-09

* kqueue: directory watching improvements (thanks @vmirage)

## r2012-11-01

* inotify: add `IN_MOVED_TO` #25 (requested by @cpisto)
* [Fix] kqueue: deleting watched directory #24 (reported by @jakerr)

## r2012-10-09

* [Fix] inotify: fixes from https://codereview.appspot.com/5418045/ (ugorji)
* [Fix] kqueue: preserve watch flags when watching for delete #21 (reported by @robfig)
* [Fix] kqueue: watch the directory even if it isn't a new watch (thanks @robfig)
* [Fix] kqueue: modify after recreation of file

## r2012-09-27

* [Fix] kqueue: watch with an existing folder inside the watched folder (thanks @vmirage)
* [Fix] kqueue: no longer get duplicate CREATE events

## r2012-09-01

* kqueue: events for created directories

## r2012-07-14

* [Fix] for renaming files

## r2012-07-02

* [Feature] FSNotify flags
* [Fix] inotify: Added file name back to event path

## r2012-06-06

* kqueue: watch files after directory created (thanks @tmc)

## r2012-05-22

* [Fix] inotify: remove all watches before Close()

## r2012-05-03

* [API] kqueue: return errors during watch instead of sending over channel

## r2012-04-28

* kqueue: match symlink behavior on Linux
* inotify: add `DELETE_SELF` (requested by @taralx)
* [Doc] Godoc example (thanks @davecheney)
* [Fix] kqueue: handle EINTR (reported by @robfig)

## r2012-03-30

* Go 1 released: build with go tool
* [Feature] Windows support using winfsnotify
* Windows does not have attribute change notifications
* Roll attribute notifications into IsModify

## r2012-02-19

* kqueue: add files when watch directory

## r2011-12-30

* update to latest Go weekly code

## r2011-10-19

* kqueue: add watch on file creation to match inotify
* kqueue: create file event
* inotify: ignore `IN_IGNORED` events
* event String()
* linux: common FileEvent functions
* initial commit
