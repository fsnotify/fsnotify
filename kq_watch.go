package fsnotify

import (
	"fmt"
	"io/fs"
	"syscall"
)

type (
	watches struct {
		wd   map[int]watch  // wd → watch
		path map[string]int // pathname → wd
	}
	watch struct {
		wd          int
		ident       [2]uint64 // inode, dev
		withOp      Op
		path        string
		fflags      int
		isdir       bool
		recurse     bool
		watchingDir bool // Watching the contents of this dir.
		byUser      bool
	}
)

func (w watch) String() string {
	return fmt.Sprintf("wd=%-4d watchingDir=%-5v  withOp=%v  path=%q",
		w.wd, w.watchingDir, w.withOp, w.path)
}

func newWatches() watches {
	return watches{
		wd:   make(map[int]watch),
		path: make(map[string]int),
		//byDir:  make(map[string]map[int]struct{}),
		//seen:   make(map[string]struct{}),
		//byUser: make(map[string]struct{}),
	}
}

func (w *watches) add(path string, wd int, fi fs.FileInfo, fflags int, watchingDir bool, withOp Op, byUser bool) {
	var (
		dir   bool
		ident [2]uint64
	)
	if fi != nil {
		dir = fi.IsDir()
		sys := fi.Sys().(*syscall.Stat_t)
		ident[0], ident[1] = sys.Dev, sys.Ino
	}
	w.wd[wd] = watch{
		wd:          wd,
		ident:       ident,
		path:        path,
		fflags:      fflags,
		isdir:       dir,
		watchingDir: watchingDir,
		withOp:      withOp,
		byUser:      byUser,
	}
	w.path[path] = wd
}

func (w *watches) update(watch watch) {
	w.wd[watch.wd] = watch
}

func (w *watches) remove(watch watch) {
	delete(w.path, watch.path)
	delete(w.wd, watch.wd)

	//isDir := w.wd[fd].isDir
	//delete(w.byUser, path)

	// parent := filepath.Dir(path)
	// delete(w.byDir[parent], fd)
	// if len(w.byDir[parent]) == 0 {
	// 	delete(w.byDir, parent)
	// }

	//delete(w.seen, path)
}

func (w *watches) byWd(wd int) watch {
	return w.wd[wd]
}

func (w *watches) byPath(path string) (watch, bool) {
	info, ok := w.wd[w.path[path]]
	return info, ok
}
