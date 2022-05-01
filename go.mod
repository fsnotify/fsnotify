module github.com/burakgulbay/fsnotifier

go 1.16

require golang.org/x/sys v0.0.0-20220429233432-b5fbb4746d32

retract (
	v1.5.3 // Published an incorrect branch accidentally https://github.com/burakgulbay/fsnotifier/issues/445
	v1.5.0 // Contains symlink regression https://github.com/burakgulbay/fsnotifier/pull/394
)
