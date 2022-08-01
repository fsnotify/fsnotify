//go:build !plan9
// +build !plan9

package fsnotify

import (
	"testing"
)

func TestEventString(t *testing.T) {
	tests := []struct {
		in   Event
		want string
	}{
		{Event{}, `"": `},
		{Event{"/file", 0}, `"/file": `},

		{Event{"/file", Chmod | Create},
			`"/file": CREATE|CHMOD`},
		{Event{"/file", Rename},
			`"/file": RENAME`},
		{Event{"/file", Remove},
			`"/file": REMOVE`},
		{Event{"/file", Write | Chmod},
			`"/file": WRITE|CHMOD`},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			have := tt.in.String()
			if have != tt.want {
				t.Errorf("\nhave: %q\nwant: %q", have, tt.want)
			}
		})
	}
}
