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
	"strconv"
	"strings"
	"testing"
	"time"

	"tailscale.com/derp/derphttp"
	"tailscale.com/net/dnscache"
	"tailscale.com/net/netmon"
	"tailscale.com/tailcfg"
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

func TestDesktopTLSDERPIPKeepsVerificationHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	trusted := x509.NewCertPool()
	trusted.AddCert(server.Certificate())
	monitor := netmon.NewStatic()
	defer monitor.Close()
	for _, test := range []struct {
		name, identity string
		roots          *x509.CertPool
		wantError      bool
	}{
		{"trusted relay", host, trusted, false},
		{"wrong relay IP", "192.0.2.10", trusted, true},
		{"untrusted relay", host, x509.NewCertPool(), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := derphttp.NewNetcheckClient(t.Logf, monitor)
			client.TLSConfig = &tls.Config{RootCAs: test.roots, MinVersion: tls.VersionTLS12}
			region := &tailcfg.DERPRegion{RegionID: 998, Nodes: []*tailcfg.DERPNode{{Name: "relay", RegionID: 998, HostName: test.identity, IPv4: host, IPv6: "none", DERPPort: port}}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			connection, closer, _, err := client.DialRegionTLS(ctx, region)
			if closer != nil {
				closer.Close()
			}
			if connection != nil {
				connection.Close()
			}
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v; want error = %v", err, test.wantError)
			}
			if test.name == "untrusted relay" {
				var trustError x509.UnknownAuthorityError
				if !errors.As(err, &trustError) {
					t.Fatalf("not a trust rejection: %v", err)
				}
			}
		})
	}
}
