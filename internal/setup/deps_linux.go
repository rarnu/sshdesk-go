//go:build linux

package setup

import "io"

// DefaultDeps wires the Linux installer to the real operating system.
func DefaultDeps(stdout, stderr io.Writer) Deps {
	return unixDeps(stdout, stderr)
}

func install(d Deps, opts InstallOptions) int {
	return linuxInstall(d, opts, defaultLinuxPaths())
}

func uninstall(d Deps, opts UninstallOptions) int {
	return linuxUninstall(d, opts, defaultLinuxPaths())
}
