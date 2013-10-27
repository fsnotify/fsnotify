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

