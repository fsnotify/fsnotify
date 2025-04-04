//go:build freebsd

package fsnotify

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Only on FreeBSD; need to update x/sys to include this. Not so easy to do this
// for all architectures, as it needs a FreeBSD machine for them 🤔
//
// Note this is only since FreeBSD 14.
const o_path = 0x00400000

const openMode = unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC

var openNofollow = func() int {
	var n unix.Utsname
	unix.Uname(&n)
	v, _, ok := strings.Cut(string(n.Release[:]), ".")
	if !ok {
		return 0
	}
	vv, _ := strconv.Atoi(v)
	if vv < 13 {
		return 0
	}
	return o_path | unix.O_NOFOLLOW
}()
