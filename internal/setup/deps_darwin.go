//go:build darwin

package setup

import (
	"io"
	"os"
)

// DefaultDeps wires the macOS installer to the real operating system.
func DefaultDeps(stdout, stderr io.Writer) Deps {
	return unixDeps(stdout, stderr)
}

func install(d Deps, opts InstallOptions) int {
	home, err := os.UserHomeDir()
	if err != nil {
		d.errf("could not locate the home directory: %s", err)
		return 1
	}
	return darwinInstall(d, opts, defaultDarwinPaths(home))
}

func uninstall(d Deps, opts UninstallOptions) int {
	home, err := os.UserHomeDir()
	if err != nil {
		d.errf("could not locate the home directory: %s", err)
		return 1
	}
	return darwinUninstall(d, opts, defaultDarwinPaths(home))
}
