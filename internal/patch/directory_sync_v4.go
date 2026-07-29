//go:build !windows

package patch

import "errors"

func syncDirectoryV4(root *installationRoot, path string) error {
	if path == "" {
		path = "."
	}
	directory, err := root.root.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
