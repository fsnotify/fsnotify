package internal

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func Debug(name string, mask uint32) {
	names := []struct {
		n string
		m uint32
	}{
		//{"FILE_NOTIFY_CHANGE_FILE_NAME", windows.FILE_NOTIFY_CHANGE_FILE_NAME},
		//{"FILE_NOTIFY_CHANGE_DIR_NAME", windows.FILE_NOTIFY_CHANGE_DIR_NAME},
		//{"FILE_NOTIFY_CHANGE_ATTRIBUTES", windows.FILE_NOTIFY_CHANGE_ATTRIBUTES},
		//{"FILE_NOTIFY_CHANGE_SIZE", windows.FILE_NOTIFY_CHANGE_SIZE},
		//{"FILE_NOTIFY_CHANGE_LAST_WRITE", windows.FILE_NOTIFY_CHANGE_LAST_WRITE},
		//{"FILE_NOTIFY_CHANGE_LAST_ACCESS", windows.FILE_NOTIFY_CHANGE_LAST_ACCESS},
		//{"FILE_NOTIFY_CHANGE_CREATION", windows.FILE_NOTIFY_CHANGE_CREATION},
		//{"FILE_NOTIFY_CHANGE_SECURITY", windows.FILE_NOTIFY_CHANGE_SECURITY},
		{"FILE_ACTION_ADDED", windows.FILE_ACTION_ADDED},
		{"FILE_ACTION_REMOVED", windows.FILE_ACTION_REMOVED},
		{"FILE_ACTION_MODIFIED", windows.FILE_ACTION_MODIFIED},
		{"FILE_ACTION_RENAMED_OLD_NAME", windows.FILE_ACTION_RENAMED_OLD_NAME},
		{"FILE_ACTION_RENAMED_NEW_NAME", windows.FILE_ACTION_RENAMED_NEW_NAME},
	}

	var (
		l       []string
		unknown = mask
	)
	for _, n := range names {
		if mask&n.m == n.m {
			l = append(l, n.n)
			unknown ^= n.m
		}
	}
	if unknown > 0 {
		l = append(l, fmt.Sprintf("0x%x", unknown))
	}
	fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  %2d:%-65s → %q\n",
		time.Now().Format("15:04:05.000000000"), mask, strings.Join(l, " | "), name)
}
