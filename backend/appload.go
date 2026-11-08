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
	maxBody int // largest message the socket takes in one record
	writeMu sync.Mutex
}

// sendBuffer is what we ask for: big pages are sent as one message. The
// kernel's default (about 200 kB) is too small for long articles.
const sendBuffer = 16 << 20

func ConnectAppLoad(path string) (*AppLoad, error) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}
	if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("connect %s: %w", path, err)
	}
	// SO_SNDBUFFORCE works as root, i.e. before the sandbox; else try the
	// normal option, which the kernel caps at net.core.wmem_max.
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUFFORCE, sendBuffer); err != nil {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, sendBuffer)
	}
	maxBody := 200 << 10
	if n, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF); err == nil && n > 4096 {
		maxBody = n - 4096 // the kernel keeps some of it for bookkeeping
	}
	if maxBody > maxMessageLength {
		maxBody = maxMessageLength
	}
	return &AppLoad{fd: fd, maxBody: maxBody}, nil
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

// ErrTooLarge: the message doesn't fit into one socket record. Nothing was
// sent (a header without its body would confuse the frontend).
var ErrTooLarge = errors.New("message too large")

// Send is safe for concurrent use.
func (a *AppLoad) Send(typ int32, contents string) error {
	body := []byte(contents)
	if len(body) > a.maxBody {
		return fmt.Errorf("%w (%d bytes, at most %d)", ErrTooLarge, len(body), a.maxBody)
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
