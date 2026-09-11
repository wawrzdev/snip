//go:build !darwin && !linux

package snip

import "os"

func isTerminal(_ *os.File) bool { return false }
