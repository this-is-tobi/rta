//go:build !unix

package git

import "errors"

func mkfifo(string) error { return errors.New("named pipes are a unix file type") }
