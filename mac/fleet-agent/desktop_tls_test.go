//go:build fleet_desktop

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tailscale.com/net/dnscache"
)

func TestDesktopTLSIPKeepsVerificationHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "https://")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	trusted := x509.NewCertPool()
	trusted.AddCert(server.Certificate())
	for _, test := range []struct {
		name, expectedHost string
		roots              *x509.CertPool
		wantError          bool
	}{
		{"trusted IP with host-scoped trust", host, trusted, false},
		{"wrong IP remains rejected", "192.0.2.10", trusted, true},
		{"untrusted certificate remains rejected", host, x509.NewCertPool(), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &tls.Config{ServerName: test.expectedHost, MinVersion: tls.VersionTLS12}
			// Like tlsdial, validate in the callback. Host-scoped macOS trust
			// requires the dialed IP even though TLS omits IP addresses from SNI.
			base.InsecureSkipVerify = true
			base.VerifyConnection = func(state tls.ConnectionState) error {
				if state.ServerName != test.expectedHost {
					return errors.New("verification lost the expected IP hostname")
				}
				_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
					DNSName: state.ServerName, Roots: test.roots,
				})
				return err
			}
			dial := dnscache.TLSDialer((&net.Dialer{Timeout: time.Second}).DialContext, &dnscache.Resolver{}, base)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			connection, err := dial(ctx, "tcp", address)
			if connection != nil {
				connection.Close()
			}
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v; want error = %v", err, test.wantError)
			}
			if test.name == "wrong IP remains rejected" {
				var hostnameError x509.HostnameError
				if !errors.As(err, &hostnameError) {
					t.Fatalf("expected an IP identity rejection, got %v", err)
				}
			}
			if test.name == "untrusted certificate remains rejected" {
				var authorityError x509.UnknownAuthorityError
				if !errors.As(err, &authorityError) {
					t.Fatalf("expected an untrusted issuer rejection, got %v", err)
				}
			}
			if base.ServerName != test.expectedHost {
				t.Fatal("mutated shared TLS configuration")
			}
		})
	}
}
