//go:build linux

package setup

import (
	"os"
	"syscall"
)

// procOwnerUid reads the numeric owner of a /proc/<pid> directory.
func procOwnerUid(info os.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}

// harvestGraphicalSession collects the session environment from the target
// account's active graphical session via /proc.
func harvestGraphicalSession(uid int) map[string]string {
	return scanSessionProc("/proc", uid, procOwnerUid)
}
