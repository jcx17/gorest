package gorest_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorest/gorest"
)

var _ = Describe("Client", func() {
	var (
		testServer *httptest.Server
	)

	BeforeEach(func() {
		testServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/json":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"message": "ok"}`)
			case "/text":
				fmt.Fprint(w, "Hello, World!")
			case "/stream":
				// Simulate streaming by sending several chunks.
				for i := 0; i < 3; i++ {
					fmt.Fprintf(w, "chunk%d\n", i)
					time.Sleep(10 * time.Millisecond)
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	})

	AfterEach(func() {
		testServer.Close()
	})

	It("should create a new client with default settings", func() {
		client := gorest.NewClient()
		Expect(client).NotTo(BeNil())
	})

	It("should respect custom timeout", func() {
		// Create a client with a very short timeout.
		client := gorest.NewClient(gorest.WithTimeout(50 * time.Millisecond))
		// Create a server that sleeps longer than the timeout.
		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			fmt.Fprint(w, "slow")
		}))
		defer slowServer.Close()

		req := gorest.NewRequest("GET", slowServer.URL)
		_, err := client.Do(context.Background(), req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Client.Timeout"))
	})

	It("should perform a GET request and read the full response", func() {
		client := gorest.NewClient()
		req := gorest.NewRequest("GET", testServer.URL+"/text")
		resp, err := client.Do(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		// Use the Bytes() helper to read the auto-read, auto-closed response.
		body, err := resp.Bytes()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("Hello, World!"))
	})

	It("should perform a streaming request and allow manual reading", func() {
		client := gorest.NewClient()
		req := gorest.NewRequest("GET", testServer.URL+"/stream")
		resp, err := client.DoStream(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		// Caller is responsible for closing the response.
		defer resp.Close()

		var chunks []string
		err = resp.StreamChunks(func(chunk []byte) {
			chunks = append(chunks, string(chunk))
		})
		Expect(err).NotTo(HaveOccurred())

		// Combine all chunks (whether one or several) into one string.
		combined := ""
		for _, s := range chunks {
			combined += s
		}
		// The test server writes: "chunk0\nchunk1\nchunk2\n"
		Expect(combined).To(Equal("chunk0\nchunk1\nchunk2\n"))
	})

	It("should perform GET convenience method", func() {
		client := gorest.NewClient()
		headers := map[string]string{"X-Test": "value"}
		resp, err := client.Get(context.Background(), testServer.URL+"/text", headers)
		Expect(err).NotTo(HaveOccurred())
		body, err := resp.Bytes()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("Hello, World!"))
	})

	It("should perform POST convenience method", func() {
		// Create a server that echoes the POST body.
		echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			fmt.Fprint(w, string(b))
		}))
		defer echoServer.Close()

		client := gorest.NewClient()
		payload := []byte("post data")
		resp, err := client.Post(context.Background(), echoServer.URL, payload, nil)
		Expect(err).NotTo(HaveOccurred())
		body, err := resp.Bytes()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("post data"))
	})
})
