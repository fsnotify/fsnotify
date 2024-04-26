//go:build freebsd || openbsd || netbsd || dragonfly || darwin

package internal

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func Debug(name string, kevent *unix.Kevent_t) {
	mask := uint32(kevent.Fflags)
	var l []string
	for _, n := range names {
		if mask&n.m == n.m {
			l = append(l, n.n)
		}
	}
	fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  %10d:%-60s → %q\n",
		time.Now().Format("15:04:05.000000000"), mask, strings.Join(l, " | "), name)
}
