//go:build !darwin && !linux

package snip

import (
	"errors"
	"os"
)

func openPTYForTest() (*os.File, error) { return nil, errors.New("PTY test unsupported") }
