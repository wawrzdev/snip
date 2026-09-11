//go:build darwin

package snip

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func openPTYForTest() (*os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		_ = master.Close()
		return nil, err
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(syscall.TIOCPTYGRANT), 0); errno != 0 {
		return fail(errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(syscall.TIOCPTYUNLK), 0); errno != 0 {
		return fail(errno)
	}
	var name [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(syscall.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		return fail(errno)
	}
	end := bytes.IndexByte(name[:], 0)
	if end < 0 {
		return fail(fmt.Errorf("PTY name is not terminated"))
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR, 0)
	_ = master.Close()
	return slave, err
}
