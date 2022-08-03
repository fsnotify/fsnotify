module github.com/fsnotify/fsnotify

go 1.16

require golang.org/x/sys v0.0.0-20220731174439-a90be440212d

retract (
	v1.5.3 // Published an incorrect branch accidentally https://github.com/fsnotify/fsnotify/issues/445
	v1.5.0 // Contains symlink regression https://github.com/fsnotify/fsnotify/pull/394
)
