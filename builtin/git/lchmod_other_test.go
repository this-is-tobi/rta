//go:build !darwin

package git

import "errors"

func lchmod(string, uint32) error {
	return errors.New("a symbolic link's own mode is a macOS file attribute")
}
