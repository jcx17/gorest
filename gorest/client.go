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
		timeout:     30 * time.Second,
		autoBuffer:  true,
	}
	for _, opt := range options {
		opt(c)
	}

	// Determine base RoundTripper
	baseRt := http.DefaultTransport
	if c.client != nil && c.client.Transport != nil {
		baseRt = c.client.Transport
	} else if c.rt != nil {
		baseRt = c.rt
	}

	// Wrap with middleware
	wrappedRt := c.wrapTransport(baseRt)

	if c.client == nil {
		c.client = &http.Client{
			Transport: wrappedRt,
			Timeout:   c.timeout,
		}
	} else {
		c.client.Transport = wrappedRt
		c.client.Timeout = c.timeout
	}

	return c
}

func (c *Client) wrapTransport(base http.RoundTripper) http.RoundTripper {
	return RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		final := func(req *http.Request) (*http.Response, error) {
			return base.RoundTrip(req)
		}
		chain := ChainMiddlewares(final, c.middlewares...)
		return chain(req)
	})
}

// WithHTTPClient sets a custom *http.Client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.client = client
	}
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

// DoAsync sends the HTTP request asynchronously. It launches a goroutine
// that calls the synchronous Do method and writes the result into a channel.
// The channel is buffered so the goroutine will not block if the result is not immediately read.
func (c *Client) DoAsync(ctx context.Context, req *Request) <-chan AsyncResponse {
	responseChan := make(chan AsyncResponse, 1)
	go func() {
		defer close(responseChan)
		res, err := c.Do(ctx, req)
		responseChan <- AsyncResponse{Response: res, Error: err}
	}()
	return responseChan
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

// DoStreamAsync is similar to DoAsync but uses the DoStream method to allow manual streaming.
func (c *Client) DoStreamAsync(ctx context.Context, req *Request) <-chan AsyncResponse {
	responseChan := make(chan AsyncResponse, 1)
	go func() {
		res, err := c.DoStream(ctx, req)
		responseChan <- AsyncResponse{Response: res, Error: err}
	}()
	return responseChan
}

// DoGroupAsync fires off multiple asynchronous HTTP requests concurrently,
// one for each provided *Request, and returns a channel that will eventually
// yield a slice of AsyncResult (one per request).
func (c *Client) DoGroupAsync(ctx context.Context, requests ...*Request) <-chan []AsyncResponse {
	// Create a slice of async result channels—one per request.
	channels := make([]<-chan AsyncResponse, len(requests))
	for i, req := range requests {
		channels[i] = c.DoAsync(ctx, req)
	}
	// Use the join helper to aggregate all results.
	return c.JoinAsyncResponses(channels...)
}

// JoinAsyncResponses accepts multiple AsyncResult channels and returns a channel that will emit
// a slice of AsyncResult once all the provided async operations have completed.
func (c *Client) JoinAsyncResponses(channels ...<-chan AsyncResponse) <-chan []AsyncResponse {
	out := make(chan []AsyncResponse, 1)
	go func() {
		results := make([]AsyncResponse, len(channels))
		// Wait for each async result to complete.
		for i, ch := range channels {
			results[i] = <-ch
		}
		out <- results
	}()
	return out
}

// Get is a convenience method for sending GET requests.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (*Response, error) {
	req := NewRequest("GET", url).WithHeaders(headers)
	return c.Do(ctx, req)
}

// GetAsync is a convenience wrapper for asynchronous GET requests.
func (c *Client) GetAsync(ctx context.Context, url string, headers map[string]string) <-chan AsyncResponse {
	req := NewRequest("GET", url).WithHeaders(headers)
	return c.DoAsync(ctx, req)
}

// Post is a convenience method for sending POST requests.
func (c *Client) Post(ctx context.Context, url string, body []byte, headers map[string]string) (*Response, error) {
	req := NewRequest("POST", url).WithBody(body).WithHeaders(headers)
	return c.Do(ctx, req)
}

// PostAsync is a convenience wrapper for asynchronous POST requests.
func (c *Client) PostAsync(ctx context.Context, url string, body []byte, headers map[string]string) <-chan AsyncResponse {
	req := NewRequest("POST", url).WithBody(body).WithHeaders(headers)
	return c.DoAsync(ctx, req)
}
