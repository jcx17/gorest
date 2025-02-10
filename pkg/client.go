package gorest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// Client is a configurable API client that supports middleware chaining and request building.
type Client struct {
	client      *http.Client
	rt          http.RoundTripper
	middlewares []Middleware
	timeout     time.Duration
	// autoBuffer controls whether non-streaming responses are fully read into memory.
	autoBuffer bool
}

// Option defines a function signature for configuring the Client.
type Option func(*Client)

// NewClient creates a new API client with default settings (30-second timeout, auto-buffering enabled),
// optionally configured by provided options.
func NewClient(options ...Option) *Client {
	c := &Client{
		rt:          http.DefaultTransport,
		middlewares: []Middleware{},
		timeout:     30 * time.Second, // default timeout
		autoBuffer:  true,             // default fully buffer non-streaming responses
	}
	for _, opt := range options {
		opt(c)
	}
	// If a custom HTTP client was provided via WithHTTPClient
	if c.client == nil {
		c.rt = c.wrapTransport()
		c.client = &http.Client{
			Transport: c.rt,
			Timeout:   c.timeout,
		}
	} else {
		if c.client.Transport == nil {
			c.client.Transport = c.wrapTransport()
		}
	}
	return c
}

// wrapTransport wraps the underlying RoundTripper with the middleware chain.
func (c *Client) wrapTransport() http.RoundTripper {
	orig := c.rt
	return RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		final := func(req *http.Request) (*http.Response, error) {
			return orig.RoundTrip(req)
		}
		chain := ChainMiddlewares(final, c.middlewares...)
		return chain(req)
	})
}

// WithTransport sets a custom http.RoundTripper for the client.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *Client) {
		c.rt = rt
	}
}

// WithTimeout sets the timeout for HTTP requests.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// WithMiddlewares adds one or more middleware functions to the client.
func WithMiddlewares(mws ...Middleware) Option {
	return func(c *Client) {
		c.middlewares = append(c.middlewares, mws...)
	}
}

// WithAutoBufferResponse configures whether the non-streaming Do method fully buffers the response into memory.
// Set to false if you wish to handle the response stream manually. Defaults to true.
func WithAutoBufferResponse(autoBuffer bool) Option {
	return func(c *Client) {
		c.autoBuffer = autoBuffer
	}
}

// Do sends the HTTP request built from the provided Request and returns a Response.
// For non-streaming requests, the full response is read into memory (if autoBuffer is true).
func (c *Client) Do(ctx context.Context, req *Request) (res *Response, err error) {
	httpReq, err := req.BuildHTTPRequest()
	if err != nil {
		return nil, err
	}
	httpReq = httpReq.WithContext(ctx)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if c.autoBuffer {
		// For non-streaming requests, read the full response and replace the body.
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return &Response{Response: &http.Response{
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       io.NopCloser(bytes.NewReader(body)),
		}}, nil
	}
	// If autoBuffer is disabled, return the raw response.
	return &Response{Response: resp}, nil
}

// DoStream sends the HTTP request built from the provided Request and returns a Response
// for manual streaming. The caller is responsible for closing the response.
func (c *Client) DoStream(ctx context.Context, req *Request) (*Response, error) {
	httpReq, err := req.BuildHTTPRequest()
	if err != nil {
		return nil, err
	}
	httpReq = httpReq.WithContext(ctx)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	// The caller should use methods like StreamChunks() to process the response.
	return &Response{Response: resp}, nil
}

// Get is a convenience method for sending GET requests.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (*Response, error) {
	req := NewRequest("GET", url).WithHeaders(headers)
	return c.Do(ctx, req)
}

// Post is a convenience method for sending POST requests.
func (c *Client) Post(ctx context.Context, url string, body []byte, headers map[string]string) (*Response, error) {
	req := NewRequest("POST", url).WithBody(body).WithHeaders(headers)
	return c.Do(ctx, req)
}
