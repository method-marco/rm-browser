package main

// Minimal client for the AppLoad backend protocol (asivery/rm-appload).
//
// AppLoad creates a listening AF_UNIX / SOCK_SEQPACKET socket and passes its
// path as argv[1]. The backend connects to it. Every message is two
// SEQPACKET records:
//
//	header: struct { int32 type; int32 length; }  (native endian, LE on ARM)
//	body:   `length` bytes of UTF-8 (omitted when length == 0)
//
// Types -1, -2 and -3 are reserved system messages (see src/protocol.h).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"syscall"
)

const (
	msgSystemTerminate       int32 = -1
	msgSystemNewCoordinator  int32 = -2
	msgSystemLostCoordinator int32 = -3
	maxMessageLength               = 10 * 1024 * 1024
)

type AppLoad struct {
	fd      int
	writeMu sync.Mutex
}

func ConnectAppLoad(path string) (*AppLoad, error) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}
	if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("connect %s: %w", path, err)
	}
	return &AppLoad{fd: fd}, nil
}

func (a *AppLoad) Close() error { return syscall.Close(a.fd) }

// Receive blocks until the next message arrives.
func (a *AppLoad) Receive() (int32, string, error) {
	hdr := make([]byte, 8)
	n, err := syscall.Read(a.fd, hdr)
	if err != nil {
		return 0, "", err
	}
	if n == 0 {
		return 0, "", errors.New("appload socket closed")
	}
	if n != 8 {
		return 0, "", fmt.Errorf("short header: %d bytes", n)
	}
	typ := int32(binary.LittleEndian.Uint32(hdr[0:4]))
	length := int32(binary.LittleEndian.Uint32(hdr[4:8]))
	if length < 0 || length > maxMessageLength {
		return 0, "", fmt.Errorf("invalid message length %d", length)
	}
	if length == 0 {
		return typ, "", nil
	}
	body := make([]byte, length)
	n, err = syscall.Read(a.fd, body)
	if err != nil {
		return 0, "", err
	}
	return typ, string(body[:n]), nil
}

// Send is safe for concurrent use.
func (a *AppLoad) Send(typ int32, contents string) error {
	body := []byte(contents)
	if len(body) > maxMessageLength {
		return fmt.Errorf("message too long (%d bytes)", len(body))
	}
	hdr := make([]byte, 8)
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(typ))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(body)))

	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if _, err := syscall.Write(a.fd, hdr); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := syscall.Write(a.fd, body); err != nil {
			return err
		}
	}
	return nil
}
