package gorest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorest/pkg"
)

var _ = Describe("Middleware", func() {

	Describe("ChainMiddlewares", func() {
		It("should chain middleware in the correct order", func() {
			// Create two middleware that append letters to a header "X-Test".
			mw1 := func(next gorest.RoundTripFunc) gorest.RoundTripFunc {
				return func(req *http.Request) (*http.Response, error) {
					prev := req.Header.Get("X-Test")
					req.Header.Set("X-Test", prev+"A")
					return next(req)
				}
			}
			mw2 := func(next gorest.RoundTripFunc) gorest.RoundTripFunc {
				return func(req *http.Request) (*http.Response, error) {
					prev := req.Header.Get("X-Test")
					req.Header.Set("X-Test", prev+"B")
					return next(req)
				}
			}
			// Final function returns a response with header "X-Test" copied from the request.
			final := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"X-Test": {req.Header.Get("X-Test")}},
					Body:       io.NopCloser(strings.NewReader("ok")),
				}, nil
			})

			chained := gorest.ChainMiddlewares(final, mw1, mw2)

			req, err := http.NewRequest("GET", "http://example.com", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("X-Test", "")

			resp, err := chained(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Header.Get("X-Test")).To(Equal("AB"))
		})
	})

	Describe("RetryMiddleware", func() {
		It("should return a context error if the request is cancelled", func() {
			// Create a request with a cancelled context.
			req, err := http.NewRequest("GET", "http://example.com", nil)
			Expect(err).NotTo(HaveOccurred())
			ctx, cancel := context.WithCancel(req.Context())
			cancel() // cancel immediately
			req = req.WithContext(ctx)

			// Dummy round-trip function that would return success if called.
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("ok")),
				}, nil
			})

			mw := gorest.RetryMiddleware(3, 1*time.Millisecond)
			wrapped := mw(dummy)
			_, err = wrapped(req)
			Expect(err).To(MatchError(context.Canceled))
		})

		It("should retry on error and eventually succeed", func() {
			var callCount int32
			// Dummy round-trip: first call returns an error, second call returns success.
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if atomic.AddInt32(&callCount, 1) == 1 {
					return nil, errors.New("temporary error")
				}
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("success")),
				}, nil
			})

			bodyContent := "request body"
			req, err := http.NewRequest("GET", "http://example.com", strings.NewReader(bodyContent))
			Expect(err).NotTo(HaveOccurred())
			req = req.WithContext(context.Background())

			mw := gorest.RetryMiddleware(3, 1*time.Millisecond)
			wrapped := mw(dummy)
			resp, err := wrapped(req)
			Expect(err).NotTo(HaveOccurred())
			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(Equal("success"))
			Expect(atomic.LoadInt32(&callCount)).To(Equal(int32(2)))
		})

		It("should retry on a 429 response with a valid Retry-After header", func() {
			var callCount int32
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if atomic.AddInt32(&callCount, 1) == 1 {
					// Return 429 with Retry-After header set to "0" (zero seconds).
					return &http.Response{
						StatusCode: 429,
						Header:     http.Header{"Retry-After": {"0"}},
						Body:       io.NopCloser(strings.NewReader("rate limit")),
					}, nil
				}
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("ok")),
				}, nil
			})

			req, err := http.NewRequest("GET", "http://example.com", strings.NewReader("data"))
			Expect(err).NotTo(HaveOccurred())
			req = req.WithContext(context.Background())

			mw := gorest.RetryMiddleware(3, 1*time.Millisecond)
			wrapped := mw(dummy)
			resp, err := wrapped(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
		})

		It("should retry on 500 status code", func() {
			var callCount int32
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if atomic.AddInt32(&callCount, 1) == 1 {
					return &http.Response{
						StatusCode: 500,
						Body:       io.NopCloser(strings.NewReader("server error")),
					}, nil
				}
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("ok")),
				}, nil
			})

			req, err := http.NewRequest("GET", "http://example.com", strings.NewReader("data"))
			Expect(err).NotTo(HaveOccurred())
			req = req.WithContext(context.Background())

			mw := gorest.RetryMiddleware(3, 1*time.Millisecond)
			wrapped := mw(dummy)
			resp, err := wrapped(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
		})

		It("should return an error if all retries fail", func() {
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("fail")
			})

			req, err := http.NewRequest("GET", "http://example.com", nil)
			Expect(err).NotTo(HaveOccurred())
			req = req.WithContext(context.Background())

			mw := gorest.RetryMiddleware(1, 1*time.Millisecond)
			wrapped := mw(dummy)
			resp, err := wrapped(req)
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
		})

		It("should propagate error if reading the body fails", func() {
			// Create a request with a Body that always errors.
			req, err := http.NewRequest("GET", "http://example.com", &errorReadCloser{})
			Expect(err).NotTo(HaveOccurred())
			req = req.WithContext(context.Background())

			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("ok")),
				}, nil
			})

			mw := gorest.RetryMiddleware(1, 1*time.Millisecond)
			wrapped := mw(dummy)
			_, err = wrapped(req)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("read error"))
		})
	})

	Describe("LoggingMiddleware", func() {
		It("should log request and response dumps", func() {
			logger := &bytes.Buffer{}
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": {"text/plain"}},
					Body:       io.NopCloser(strings.NewReader("response body")),
				}, nil
			})

			mw := gorest.LoggingMiddleware(logger)
			chained := mw(dummy)

			req, err := http.NewRequest("GET", "http://example.com", strings.NewReader("request body"))
			Expect(err).NotTo(HaveOccurred())

			resp, err := chained(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
			logOutput := logger.String()
			Expect(logOutput).To(ContainSubstring("=== Request ==="))
			Expect(logOutput).To(ContainSubstring("=== Response ==="))
		})

		It("should log a request error when next returns an error", func() {
			logger := &bytes.Buffer{}
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("dummy error")
			})

			mw := gorest.LoggingMiddleware(logger)
			chained := mw(dummy)

			req, err := http.NewRequest("GET", "http://example.com", nil)
			Expect(err).NotTo(HaveOccurred())

			_, err = chained(req)
			Expect(err).To(HaveOccurred())
			logOutput := logger.String()
			Expect(logOutput).To(ContainSubstring("=== Request Error: dummy error"))
		})

		It("should log a request dump error when DumpRequestOut fails", func() {
			logger := &bytes.Buffer{}
			// Create a request with a Body that always errors.
			req, err := http.NewRequest("GET", "http://example.com", &errorReadCloser{})
			Expect(err).NotTo(HaveOccurred())

			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader("response")),
				}, nil
			})

			mw := gorest.LoggingMiddleware(logger)
			chained := mw(dummy)

			_, err = chained(req)
			Expect(err).NotTo(HaveOccurred())
			logOutput := logger.String()
			Expect(logOutput).To(ContainSubstring("=== Request Dump Error:"))
		})

		It("should log a response dump error when DumpResponse fails", func() {
			logger := &bytes.Buffer{}
			// Create a dummy response with a Body that always errors on read.
			dummy := gorest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{},
					Body:       io.NopCloser(&errorReader{}),
				}, nil
			})

			mw := gorest.LoggingMiddleware(logger)
			chained := mw(dummy)

			req, err := http.NewRequest("GET", "http://example.com", strings.NewReader("request"))
			Expect(err).NotTo(HaveOccurred())

			_, err = chained(req)
			Expect(err).NotTo(HaveOccurred())
			logOutput := logger.String()
			Expect(logOutput).To(ContainSubstring("=== Response Dump Error:"))
		})
	})

	Describe("parseRetryAfter", func() {
		It("should parse an integer Retry-After header", func() {
			d, err := gorest.ParseRetryAfter("2")
			Expect(err).NotTo(HaveOccurred())
			Expect(d).To(Equal(2 * time.Second))
		})

		It("should parse an HTTP-date Retry-After header", func() {
			baseline := time.Date(2025, time.February, 9, 12, 0, 0, 0, time.UTC)
			future := baseline.Add(2 * time.Second)
			header := future.UTC().Format(http.TimeFormat)
			d, err := gorest.ParseRetryAfter(header, baseline)
			Expect(err).NotTo(HaveOccurred())
			Expect(d).To(Equal(2 * time.Second))
		})

		It("should return an error for an invalid Retry-After header", func() {
			_, err := gorest.ParseRetryAfter("invalid")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid Retry-After header"))
		})
	})

	Describe("drainAndClose", func() {
		It("should drain and close the response body", func() {
			closed := false
			dummyBody := &dummyReadCloser{
				Reader: strings.NewReader("dummy"),
				closeFunc: func() error {
					closed = true
					return nil
				},
			}
			resp := &http.Response{
				Body: dummyBody,
			}
			gorest.DrainAndClose(resp)
			Expect(closed).To(BeTrue())
		})
	})
})

// --- Helper types ---

// errorReadCloser always returns an error on Read.
type errorReadCloser struct{}

func (e *errorReadCloser) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func (e *errorReadCloser) Close() error {
	return nil
}

// errorReader always returns an error on Read.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

// dummyReadCloser wraps an io.Reader and calls a custom close function.
type dummyReadCloser struct {
	io.Reader
	closeFunc func() error
}

func (d *dummyReadCloser) Close() error {
	if d.closeFunc != nil {
		return d.closeFunc()
	}
	return nil
}
