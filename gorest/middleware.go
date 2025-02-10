package gorest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"time"
)

// Middleware defines a function to wrap around a RoundTripFunc.
type Middleware func(next RoundTripFunc) RoundTripFunc

// ChainMiddlewares applies a list of middleware functions around a final RoundTripFunc.
func ChainMiddlewares(final RoundTripFunc, mws ...Middleware) RoundTripFunc {
	wrapped := final
	for i := len(mws) - 1; i >= 0; i-- {
		wrapped = mws[i](wrapped)
	}
	return wrapped
}

// RetryMiddleware returns a middleware that retries a request for a total of 'attempts' times (including the first attempt)
// if errors occur or if a retryable HTTP status is received. The retryDelay is the wait time between attempts.
// Note: The request body is fully buffered in memory for retry purposes.
func RetryMiddleware(attempts int, retryDelay time.Duration) Middleware {
	return func(next RoundTripFunc) RoundTripFunc {
		return func(req *http.Request) (*http.Response, error) {
			var bodyBytes []byte
			var err error
			if req.Body != nil {
				bodyBytes, err = io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			var resp *http.Response
			for i := 0; i < attempts; i++ {
				if req.Context().Err() != nil {
					return nil, req.Context().Err()
				}

				// Clone the request for each attempt.
				reqAttempt := req.Clone(req.Context())
				if req.Body != nil {
					reqAttempt.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				}

				if i > 0 {
					time.Sleep(retryDelay)
				}

				resp, err = next(reqAttempt)
				if err != nil {
					continue
				}
				if resp.StatusCode == 429 {
					if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
						receivedAt := time.Now()
						if delay, err := ParseRetryAfter(retryAfter, receivedAt); err == nil {
							DrainAndClose(resp)
							time.Sleep(delay)
							continue
						}
					}
				} else if resp.StatusCode >= 500 {
					DrainAndClose(resp)
					time.Sleep(retryDelay)
					continue
				}
				return resp, nil
			}
			// After all attempts
			if err != nil {
				return nil, fmt.Errorf("all retry attempts failed: %w", err)
			}
			return nil, fmt.Errorf("all retry attempts exhausted, last status: %d", resp.StatusCode)
		}
	}
}

// LoggingConfig configures the LoggingMiddleware.
type LoggingConfig struct {
	// DumpRequestBody controls whether the request body is included in the log.
	DumpRequestBody bool
	// DumpResponseBody controls whether the response body is included in the log.
	DumpResponseBody bool
	// Redactor optionally allows redacting sensitive data from the log output.
	Redactor func(string) string
}

// LoggingMiddlewareWithConfig returns a middleware that logs the HTTP request and response using the provided logger and config.
// Warning: Dumping full HTTP messages may include sensitive data.
func LoggingMiddlewareWithConfig(logger io.Writer, config *LoggingConfig) Middleware {
	// Set default config if nil.
	if config == nil {
		config = &LoggingConfig{
			DumpRequestBody:  true,
			DumpResponseBody: true,
			Redactor:         func(s string) string { return s },
		}
	}
	// Ensure redactor is set.
	if config.Redactor == nil {
		config.Redactor = func(s string) string { return s }
	}
	return func(next RoundTripFunc) RoundTripFunc {
		return func(req *http.Request) (*http.Response, error) {
			var reqBody []byte
			var err error

			// Attempt to read the request body (if any)
			if req.Body != nil {
				reqBody, err = io.ReadAll(req.Body)
				if err != nil {
					// Log the error and continue with an empty body.
					_, _ = logger.Write([]byte("=== Request Dump Error: " + err.Error() + "\n"))
					reqBody = []byte{}
				}
				// Reset req.Body so it can be read downstream.
				req.Body = io.NopCloser(bytes.NewReader(reqBody))
			}

			// Attempt to dump the request.
			reqDump, dumpErr := httputil.DumpRequestOut(req, false)
			if dumpErr != nil {
				// Log the dump error instead of failing the request.
				_, _ = logger.Write([]byte("=== Request Dump Error: " + dumpErr.Error() + "\n"))
				reqDump = []byte{}
			}

			// Prepend a marker and (optionally) append the body.
			outReq := []byte("=== Request ===\n")
			outReq = append(outReq, reqDump...)
			if config.DumpRequestBody {
				outReq = append(outReq, "\nBody: "...)
				outReq = append(outReq, reqBody...)
			}
			_, _ = logger.Write([]byte(config.Redactor(string(outReq))))

			// Execute the request.
			resp, err := next(req)
			if err != nil {
				// Log the error with the expected marker.
				_, _ = logger.Write([]byte(config.Redactor("=== Request Error: " + err.Error() + "\n")))
				return resp, err
			}

			// Process the response body.
			var respBody []byte
			if resp.Body != nil {
				respBody, err = io.ReadAll(resp.Body)
				if err != nil {
					// Log the error and use an empty body.
					_, _ = logger.Write([]byte("=== Response Dump Error: " + err.Error() + "\n"))
					respBody = []byte{}
				}
				// Reset resp.Body so that downstream consumers can read it.
				resp.Body = io.NopCloser(bytes.NewReader(respBody))
			}

			// Attempt to dump the response.
			respDump, dumpErr := httputil.DumpResponse(resp, false)
			if dumpErr != nil {
				_, _ = logger.Write([]byte("=== Response Dump Error: " + dumpErr.Error() + "\n"))
				respDump = []byte{}
			}

			// Prepend the marker and (optionally) append the body.
			outResp := []byte("=== Response ===\n")
			outResp = append(outResp, respDump...)
			if config.DumpResponseBody {
				outResp = append(outResp, "\nBody: "...)
				outResp = append(outResp, respBody...)
			}
			_, _ = logger.Write([]byte(config.Redactor(string(outResp))))

			return resp, nil
		}
	}
}

// LoggingMiddleware is a convenience wrapper that logs full HTTP messages (request and response bodies).
// It uses LoggingMiddlewareWithConfig with DumpRequestBody and DumpResponseBody set to true.
func LoggingMiddleware(logger io.Writer, redactor ...func(string) string) Middleware {
	var red func(string) string
	if len(redactor) > 0 && redactor[0] != nil {
		red = redactor[0]
	} else {
		red = func(s string) string { return s }
	}
	return LoggingMiddlewareWithConfig(logger, &LoggingConfig{
		DumpRequestBody:  true,
		DumpResponseBody: true,
		Redactor:         red,
	})
}

// DrainAndClose reads the remaining data from resp.Body and closes it.
func DrainAndClose(resp *http.Response) {
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// ParseRetryAfter parses a Retry-After header value and returns the duration to wait.
// If a baseline time is provided, it is used to compute the duration for HTTP-date values.
func ParseRetryAfter(header string, baseline ...time.Time) (time.Duration, error) {
	now := time.Now()
	if len(baseline) > 0 {
		now = baseline[0]
	}

	if seconds, err := strconv.Atoi(header); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0, nil
		}
		return d, nil
	}
	return 0, fmt.Errorf("invalid Retry-After header: %s", header)
}
