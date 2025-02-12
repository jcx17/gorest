// transport_test.go
package gorest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorest/gorest"
)

var _ = Describe("TLSTransport", func() {
	var (
		tlsTrans *gorest.TLSTransport
		server   *httptest.Server
	)

	BeforeEach(func() {
		// Create a test server with TLS.
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello https"))
		}))
		// Create a TLSTransport with the server's TLS config.
		var err error
		tlsTrans, err = gorest.NewTLSTransport(
			true,
			server.Client().Transport.(*http.Transport).TLSClientConfig,
			5*time.Second,
			10,
			30*time.Second,
		)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		server.Close()
	})

	It("should successfully perform an HTTPS request using the TLSTransport", func() {
		req, err := http.NewRequest("GET", server.URL, nil)
		Expect(err).NotTo(HaveOccurred())
		resp, err := tlsTrans.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("hello https"))
	})
})
