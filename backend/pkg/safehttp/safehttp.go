// Package safehttp builds HTTP clients for fetching user-supplied URLs
// (lead websites, webhook endpoints) without enabling SSRF: connections to
// loopback, private, link-local and other non-public addresses are refused at
// dial time, after DNS resolution, so DNS rebinding cannot bypass the check.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// AllowPrivate disables the address check. Tests only.
var AllowPrivate = false

var ErrBlockedAddress = errors.New("destination address is not publicly routable")

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// IsPublic reports whether ip may be contacted.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return ip.IsValid()
}

func control(_, address string, _ syscall.RawConn) error {
	if AllowPrivate {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !IsPublic(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
	}
	return nil
}

// NewClient returns a client with timeouts, a redirect cap and the address guard.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: control}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          50,
		IdleConnTimeout:       30 * time.Second,
		Proxy:                 nil, // never route through an environment proxy
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("redirect to unsupported scheme")
			}
			return nil
		},
	}
}

// Dialer is exposed for non-HTTP protocols (IMAP/SMTP to user-configured hosts).
func Dialer() *net.Dialer {
	return &net.Dialer{Timeout: 10 * time.Second, Control: control}
}

// DialContext dials with the address guard.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return Dialer().DialContext(ctx, network, addr)
}
