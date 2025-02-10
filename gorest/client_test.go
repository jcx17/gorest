package gorest_test

import (
	"bytes"
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
				_, _ = fmt.Fprint(w, `{"message": "ok"}`)
			case "/text":
				_, _ = fmt.Fprint(w, "Hello, World!")
			case "/stream":
				// Simulate streaming by sending several chunks.
				for i := 0; i < 3; i++ {
					_, _ = fmt.Fprintf(w, "chunk%d\n", i)
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
			_, _ = fmt.Fprint(w, "slow")
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
			_, _ = fmt.Fprint(w, string(b))
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

	It("should return an error if request building fails", func() {
		client := gorest.NewClient()
		// NewRequest with an empty URL should trigger a build error.
		req := gorest.NewRequest("GET", "")
		_, err := client.Do(context.Background(), req)
		Expect(err).To(HaveOccurred())
	})

	It("should use a custom HTTP client provided via WithHTTPClient", func() {
		// Create a dummy RoundTrip function.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/custom"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("custom client response"))),
			}, nil
		})
		customClient := &http.Client{Transport: rt}
		client := gorest.NewClient(gorest.WithHTTPClient(customClient))
		req := gorest.NewRequest("GET", "http://dummy")
		resp, err := client.Do(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("custom client response"))
	})

	It("should use a custom transport provided via WithTransport", func() {
		// Create a dummy RoundTrip function that returns a 201 status.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 201,
				Header:     http.Header{"Content-Type": {"text/transport"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("custom transport response"))),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt))
		req := gorest.NewRequest("GET", "http://dummy")
		resp, err := client.Do(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(201))
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("custom transport response"))
	})

	It("should invoke middleware in proper order using WithMiddlewares", func() {
		// Create two middleware functions that append letters to a header.

		mwA := func(next gorest.RoundTripFunc) gorest.RoundTripFunc {
			return func(req *http.Request) (*http.Response, error) {
				req.Header.Set("X-Mw", req.Header.Get("X-Mw")+"A")
				return next(req)
			}
		}
		mwB := func(next gorest.RoundTripFunc) gorest.RoundTripFunc {
			return func(req *http.Request) (*http.Response, error) {
				req.Header.Set("X-Mw", req.Header.Get("X-Mw")+"B")
				return next(req)
			}
		}
		// Capture the header value inside the dummy round trip.
		var headerValue string
		dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			// Capture the header value after middleware have run.
			headerValue = req.Header.Get("X-Mw")
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"X-Mw": {headerValue}},
				Body:       io.NopCloser(bytes.NewReader([]byte("mw test response"))),
			}, nil
		})
		// Disable auto-buffering to see the header change directly.
		client := gorest.NewClient(
			gorest.WithMiddlewares(mwA, mwB),
			gorest.WithTransport(dummy),
			gorest.WithAutoBufferResponse(false),
		)
		// Reassign in case WithHeader returns a new instance.
		req := gorest.NewRequest("GET", "http://dummy")
		req = req.WithHeader("X-MW", "")
		resp, err := client.Do(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		// Verify the header captured inside the dummy is "AB"
		Expect(headerValue).To(Equal("AB"))
		// Also verify that the response header carries the expected value.
		Expect(resp.Response.Header.Get("X-Mw")).To(Equal("AB"))
	})

	It("should return the raw response when autoBuffer is disabled", func() {
		// Create a dummy round trip that returns a fixed response.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("raw response data"))),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt), gorest.WithAutoBufferResponse(false))
		req := gorest.NewRequest("GET", "http://dummy")
		resp, err := client.Do(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		// Read the body and verify its content.
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("raw response data"))
	})

	It("should handle asynchronous DoAsync calls", func() {
		// Dummy round trip that returns a fixed response.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/async"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("async response"))),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt))
		req := gorest.NewRequest("GET", "http://dummy")
		asyncChan := client.DoAsync(context.Background(), req)
		result := <-asyncChan
		Expect(result.Error).NotTo(HaveOccurred())
		body, err := io.ReadAll(result.Response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("async response"))
	})

	It("should handle asynchronous DoStreamAsync calls", func() {
		// Dummy round trip that returns a fixed streaming response.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/stream-async"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("stream async response"))),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt))
		req := gorest.NewRequest("GET", "http://dummy")
		asyncChan := client.DoStreamAsync(context.Background(), req)
		result := <-asyncChan
		Expect(result.Error).NotTo(HaveOccurred())
		body, err := io.ReadAll(result.Response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("stream async response"))
	})

	It("should handle DoGroupAsync for multiple requests", func() {
		// Dummy round trip that returns different responses based on URL.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var respText string
			switch req.URL.String() {
			case "http://dummy/1":
				respText = "group response 1"
			case "http://dummy/2":
				respText = "group response 2"
			default:
				respText = "default group response"
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(respText))),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt))
		req1 := gorest.NewRequest("GET", "http://dummy/1")
		req2 := gorest.NewRequest("GET", "http://dummy/2")
		groupChan := client.DoGroupAsync(context.Background(), req1, req2)
		results := <-groupChan
		Expect(len(results)).To(Equal(2))
		for i, res := range results {
			Expect(res.Error).NotTo(HaveOccurred())
			body, err := io.ReadAll(res.Response.Body)
			Expect(err).NotTo(HaveOccurred())
			if i == 0 {
				Expect(string(body)).To(Equal("group response 1"))
			} else if i == 1 {
				Expect(string(body)).To(Equal("group response 2"))
			}
		}
	})

	It("should join async responses with JoinAsyncResponses", func() {
		// Create two channels that emit AsyncResponse.
		ch1 := make(chan gorest.AsyncResponse, 1)
		ch2 := make(chan gorest.AsyncResponse, 1)
		ch1 <- gorest.AsyncResponse{
			Response: &gorest.Response{Response: &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader([]byte("join 1"))),
			}},
			Error: nil,
		}
		ch2 <- gorest.AsyncResponse{
			Response: &gorest.Response{Response: &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader([]byte("join 2"))),
			}},
			Error: nil,
		}
		close(ch1)
		close(ch2)
		joinChan := gorest.NewClient().JoinAsyncResponses(ch1, ch2)
		results := <-joinChan
		Expect(len(results)).To(Equal(2))
		b1, err := io.ReadAll(results[0].Response.Body)
		Expect(err).NotTo(HaveOccurred())
		b2, err := io.ReadAll(results[1].Response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b1)).To(Equal("join 1"))
		Expect(string(b2)).To(Equal("join 2"))
	})

	It("should handle GetAsync convenience method", func() {
		// Dummy round trip for GetAsync.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/getasync"}},
				Body:       io.NopCloser(bytes.NewReader([]byte("get async response"))),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt))
		asyncChan := client.GetAsync(context.Background(), "http://dummy", nil)
		result := <-asyncChan
		Expect(result.Error).NotTo(HaveOccurred())
		body, err := io.ReadAll(result.Response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("get async response"))
	})

	It("should handle PostAsync convenience method", func() {
		// Dummy round trip that echoes the request body for PostAsync.
		rt := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"text/postasync"}},
				Body:       io.NopCloser(bytes.NewReader(b)),
			}, nil
		})
		client := gorest.NewClient(gorest.WithTransport(rt))
		payload := []byte("async post data")
		asyncChan := client.PostAsync(context.Background(), "http://dummy", payload, nil)
		result := <-asyncChan
		Expect(result.Error).NotTo(HaveOccurred())
		body, err := io.ReadAll(result.Response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("async post data"))
	})
})
