package main

// HTTP client. Security comes from Go's standard library: certificate
// verification with the system roots, falling back to Mozilla's root store
// compiled into the binary (golang.org/x/crypto/x509roots/fallback) when the
// device has none; TLS 1.2 or newer only. Nothing a page sends is ever
// executed: there is no JavaScript engine, responses are only parsed.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/crypto/x509roots/fallback"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/publicsuffix"
)

const (
	userAgent      = "Mozilla/5.0 (X11; Linux aarch64; rv:140.0) Gecko/20100101 Firefox/140.0"
	acceptLanguage = "en-US,en;q=0.8"
	maxPageBytes   = 8 << 20
	maxImageBytes  = 12 << 20
)

// newHTTPClient returns a client with a fresh, in-memory cookie jar.
// Cookies are never written to disk; "Clear browsing data" replaces the jar.
func newHTTPClient() *http.Client {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second,
			ControlContext: checkDestination}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          16,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Jar:       jar,
		Transport: tr,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to unsupported address %q", req.URL.Scheme+":")
			}
			return nil
		},
	}
}

// allowLocalForTests lets tests talk to their local web server.
var allowLocalForTests = false

// webPorts are the only ports the browser connects to.
var webPorts = map[string]bool{"80": true, "443": true, "8080": true, "8443": true}

var errLocalNetwork = errors.New("this browser doesn't open addresses in your local network or on the tablet itself")

// checkDestination runs for every connection, after DNS, so a public
// name that resolves to a local address (DNS rebinding) is caught too.
// Pages must not be able to reach the router, other devices or services
// on the tablet (e.g. through an image address).
func checkDestination(ctx context.Context, network, address string, _ syscall.RawConn) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("unexpected address %q", address)
	}
	if allowLocalForTests {
		return nil
	}
	if !webPorts[port] {
		return fmt.Errorf("this browser only connects to web ports (80, 443, 8080, 8443), not %s", port)
	}
	if isLocalIP(ip) {
		return errLocalNetwork
	}
	return nil
}

var localNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "64:ff9b:1::/48",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func isLocalIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4 // also catches IPv4-mapped IPv6 addresses
	}
	for _, n := range localNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Fetched is a downloaded response body, decoded to UTF-8 for text types.
type Fetched struct {
	URL         *url.URL // final URL after redirects
	ContentType string   // media type without parameters, lower case
	Body        []byte
}

func newRequest(ctx context.Context, method, u string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", acceptLanguage)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	return req, nil
}

func fetch(ctx context.Context, c *http.Client, req *http.Request, limit int64) (*Fetched, error) {
	if s := req.URL.Scheme; s != "http" && s != "https" {
		return nil, fmt.Errorf("unsupported address %q", s+":")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, friendlyError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 && resp.StatusCode != 410 && resp.StatusCode != 403 {
		return nil, fmt.Errorf("the site answered %s", resp.Status)
	}
	mt, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	mt = strings.ToLower(mt)
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, friendlyError(err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the page is larger than %d MB", limit>>20)
	}
	if mt == "" {
		mt = strings.ToLower(strings.SplitN(http.DetectContentType(body), ";", 2)[0])
	}
	if isText(mt) {
		if r, err := charset.NewReader(strings.NewReader(string(body)), mt+"; charset="+params["charset"]); err == nil {
			if b, err := io.ReadAll(r); err == nil {
				body = b
			}
		}
	}
	if resp.StatusCode >= 400 && !isHTML(mt) {
		return nil, fmt.Errorf("the site answered %s", resp.Status)
	}
	return &Fetched{URL: resp.Request.URL, ContentType: mt, Body: body}, nil
}

func isHTML(mt string) bool { return mt == "text/html" || mt == "application/xhtml+xml" }
func isText(mt string) bool { return isHTML(mt) || strings.HasPrefix(mt, "text/") }

func friendlyError(err error) error {
	var cert *tls.CertificateVerificationError
	var dns *net.DNSError
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, errLocalNetwork):
		return errLocalNetwork
	case errors.As(err, &cert):
		return errors.New("the site's security certificate is not valid, so the page was not loaded")
	case errors.As(err, &dns):
		return fmt.Errorf("cannot find %s (no Wi-Fi, or the address is wrong)", dns.Name)
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout"):
		return errors.New("the site took too long to answer")
	}
	return err
}

// classifyInput decides whether what was typed into the address bar is an
// address (returned as a URL) or a search query (returns "").
func classifyInput(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return ""
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			return u.String()
		}
		return ""
	}
	if strings.Contains(low, "://") || strings.HasPrefix(low, "javascript:") || strings.HasPrefix(low, "data:") {
		return ""
	}
	host := s
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	if hostname == "localhost" || net.ParseIP(hostname) != nil {
		return "http://" + s
	}
	// needs a dot and a plausible top-level domain ("example.com", not "3.5")
	i := strings.LastIndex(hostname, ".")
	if i <= 0 || i == len(hostname)-1 {
		return ""
	}
	tld := hostname[i+1:]
	for _, r := range tld {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r > 127) {
			return ""
		}
	}
	if len(tld) < 2 {
		return ""
	}
	u, err := url.Parse("https://" + s)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.String()
}
