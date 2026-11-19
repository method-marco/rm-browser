package main

// KOReader progress sync (kosync, github.com/koreader/koreader-sync-server):
// the reading position of a book is shared with KOReader on other devices,
// the reMarkable's built-in reader (with kosync-native-reader), Readest
// and other kosync apps.
//
//   - a book is identified by KOReader's partial MD5 of the EPUB file, so
//     the same file is the same book everywhere
//   - positions are KOReader xpointers into the EPUB (see epub.go) plus a
//     percentage for apps that only use that
//   - the login is stored as KOReader stores it: user name and the MD5 of
//     the password ("userkey"), never the password itself

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback" // CA certificates: the sandbox has no system roots
)

const (
	defaultSyncServer = "https://sync.koreader.rocks"
	syncDeviceName    = "reMarkable Books"
)

type SyncLogin struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Userkey  string `json:"userkey"` // md5 of the password, as KOReader stores it
}

func (l SyncLogin) loggedIn() bool { return l.Username != "" && l.Userkey != "" }

type progress struct {
	Document   string  `json:"document"`
	Progress   string  `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Timestamp  int64   `json:"timestamp"`
}

var syncHTTP = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        2,
	},
}

func passwordKey(pw string) string {
	s := md5.Sum([]byte(pw))
	return hex.EncodeToString(s[:])
}

// checkServer: an http(s) address without user info; the default if empty.
func checkServer(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return defaultSyncServer, nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", errors.New("the server address is not valid")
	}
	return s, nil
}

// syncRequest returns the HTTP status (0: the server couldn't be reached)
// and the body.
func syncRequest(ctx context.Context, l SyncLogin, method, p string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(l.Server, "/")+p, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.koreader.v1+json")
	req.Header.Set("User-Agent", "rm-books")
	if l.Username != "" {
		req.Header.Set("x-auth-user", l.Username)
		req.Header.Set("x-auth-key", l.Userkey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := syncHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

func syncError(what string, code int, body []byte, err error) error {
	switch {
	case code == 0:
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return fmt.Errorf("%s failed: the sync server didn't answer.", what)
		}
		return fmt.Errorf("%s failed: can't reach the sync server. Is Wi-Fi on?", what)
	case code == 401 || code == 403:
		return fmt.Errorf("%s failed: the server didn't accept the login. Log in again in Settings.", what)
	case code >= 500:
		return fmt.Errorf("%s failed: server error (%d). Try again later.", what, code)
	}
	var m struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &m)
	if m.Message != "" {
		return fmt.Errorf("%s failed: %s", what, m.Message)
	}
	return fmt.Errorf("%s failed (HTTP %d).", what, code)
}

// syncAuth checks a login (register=true: create the account first).
func syncAuth(ctx context.Context, l SyncLogin, register bool) error {
	if register {
		code, body, err := syncRequest(ctx, SyncLogin{Server: l.Server}, "POST", "/users/create",
			map[string]string{"username": l.Username, "password": l.Userkey})
		if code != 201 && code != 200 {
			if code == 402 {
				return errors.New("Registration failed: that user name is taken.")
			}
			return syncError("Registration", code, body, err)
		}
	}
	code, body, err := syncRequest(ctx, l, "GET", "/users/auth", nil)
	if code != 200 {
		return syncError("Login", code, body, err)
	}
	return nil
}

func syncGet(ctx context.Context, l SyncLogin, digest string) (*progress, error) {
	code, body, err := syncRequest(ctx, l, "GET", "/syncs/progress/"+url.PathEscape(digest), nil)
	if code != 200 {
		return nil, syncError("Sync", code, body, err)
	}
	var p progress
	if json.Unmarshal(body, &p) != nil || (p.Progress == "" && p.Percentage == 0 && p.Timestamp == 0) {
		return nil, nil // nothing stored for this book yet
	}
	return &p, nil
}

func syncPut(ctx context.Context, l SyncLogin, p progress) (int64, error) {
	code, body, err := syncRequest(ctx, l, "PUT", "/syncs/progress", p)
	if code != 200 {
		return 0, syncError("Sync", code, body, err)
	}
	var r struct {
		Timestamp int64 `json:"timestamp"`
	}
	_ = json.Unmarshal(body, &r)
	if r.Timestamp == 0 {
		r.Timestamp = time.Now().Unix()
	}
	return r.Timestamp, nil
}

// partialMD5: KOReader's "binary" document digest (util.partialMD5): MD5 over
// 1 KiB samples at offsets 0 and 1024 << 2i (i = 0..10).
func partialMD5(r io.ReaderAt) string {
	h := md5.New()
	buf := make([]byte, 1024)
	for i := -1; i <= 10; i++ {
		var off int64
		if i >= 0 {
			off = int64(1024) << (2 * i)
		}
		n, _ := r.ReadAt(buf, off)
		if n == 0 {
			break
		}
		h.Write(buf[:n])
	}
	return hex.EncodeToString(h.Sum(nil))
}
