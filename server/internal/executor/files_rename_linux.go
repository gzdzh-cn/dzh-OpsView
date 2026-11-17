//go:build linux

package executor

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

func renameFileEntry(a *os.Root, an string, b *os.Root, bn string, noReplace bool) error {
	af, err := a.Open(".")
	if err != nil {
		return err
	}
	defer af.Close()
	bf, err := b.Open(".")
	if err != nil {
		return err
	}
	defer bf.Close()
	flags := uint(0)
	if noReplace {
		flags = unix.RENAME_NOREPLACE
	}
	return unix.Renameat2(int(af.Fd()), an, int(bf.Fd()), bn, flags)
}
func syscallEXDEV() error { return unix.EXDEV }
func hasNestedMount(path string) bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 {
			p := strings.ReplaceAll(strings.ReplaceAll(fields[4], `\040`, " "), `\134`, `\`)
			if pathContains(path, p) {
				return true
			}
		}
	}
	return false
}

func fileNoFollow() int { return unix.O_NOFOLLOW | unix.O_NONBLOCK }
