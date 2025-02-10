package gorest

import (
	"crypto/tls"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// RoundTripFunc type is an adapter to allow the use of ordinary functions as http.RoundTripper.
type RoundTripFunc func(req *http.Request) (*http.Response, error)

// RoundTripFunc type is an adapter to allow the use of ordinary functions as http.RoundTripper.
func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TLSTransport is a wrapper around http.Transport that is configured for TLS and HTTP/2.
type TLSTransport struct {
	Transport *http.Transport
}

// NewTLSTransport creates a new TLSTransport with the given TLS configuration, handshake timeout,
// maximum idle connections, and idle connection timeout. HTTP/2 is enabled for the transport.
func NewTLSTransport(tlsConfig *tls.Config, tlsHandshakeTimeout time.Duration, maxIdleCons int, idleConnTimeout time.Duration) (*TLSTransport, error) {
	tr := &http.Transport{
		TLSClientConfig:     tlsConfig,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
		MaxIdleConns:        maxIdleCons,
		IdleConnTimeout:     idleConnTimeout,
	}
	// Enable HTTP/2 for this transport.
	if err := http2.ConfigureTransport(tr); err != nil {
		return nil, err
	}
	return &TLSTransport{Transport: tr}, nil
}

// RoundTrip delegates the round-trip to the underlying Transport.
func (tt *TLSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return tt.Transport.RoundTrip(req)
}
