package world

import (
	"io/fs"
	"os"
)

// PackageDirectory is a closable filesystem rooted at one world-package
// directory. os.Root enforces the root boundary on every Open, including paths
// reached through symlinks, so every package read shares one confinement rule.
type PackageDirectory struct {
	root *os.Root
}

// OpenPackageDirectory opens a production world-package directory without
// granting package reads access to paths outside it.
func OpenPackageDirectory(name string) (*PackageDirectory, error) {
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return &PackageDirectory{root: root}, nil
}

// Open implements fs.FS.
func (d *PackageDirectory) Open(name string) (fs.File, error) { return d.root.Open(name) }

// Close releases the rooted directory handle.
func (d *PackageDirectory) Close() error { return d.root.Close() }

var _ fs.FS = (*PackageDirectory)(nil)
