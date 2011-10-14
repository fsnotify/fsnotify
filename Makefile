include $(GOROOT)/src/Make.inc

TARG=exp/fsnotify

GOFILES_linux=\
	fsnotify_linux.go\

GOFILES_freebsd=\
	fsnotify_bsd.go\

GOFILES_openbsd=\
	fsnotify_bsd.go\

GOFILES_darwin=\
	fsnotify_bsd.go\

GOFILES+=$(GOFILES_$(GOOS))

include $(GOROOT)/src/Make.pkg
