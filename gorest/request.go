package gorest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Request represents an API request with configurable headers, query parameters, and body.
type Request struct {
	method      string
	url         string
	headers     map[string]string
	queryParams url.Values
	body        io.Reader
	// Indicates whether the body was built as multipart.
	isMultipart bool
	// Holds any error encountered during body building.
	buildErr error
}

// NewRequest creates a new Request for the given method and URL.
func NewRequest(method, urlStr string) *Request {
	return &Request{
		method:      method,
		url:         urlStr,
		headers:     make(map[string]string),
		queryParams: url.Values{},
	}
}

// WithHeader adds a single header to the Request.
func (r *Request) WithHeader(key, value string) *Request {
	r.headers[key] = value
	return r
}

// WithHeaders adds multiple headers to the Request.
func (r *Request) WithHeaders(headers map[string]string) *Request {
	for k, v := range headers {
		r.headers[k] = v
	}
	return r
}

// WithQueryParam adds a query parameter to the Request.
func (r *Request) WithQueryParam(key, value string) *Request {
	r.queryParams.Add(key, value)
	return r
}

// WithBody sets the request body from a byte slice.
func (r *Request) WithBody(body []byte) *Request {
	r.body = bytes.NewReader(body)
	return r
}

// WithJSONBody sets the request body to the JSON representation of the provided data
// and sets the Content-Type header to application/json.
func (r *Request) WithJSONBody(data interface{}) *Request {
	b, err := json.Marshal(data)
	if err != nil {
		r.buildErr = err
		return r
	}
	r.body = bytes.NewReader(b)
	r.WithHeader("Content-Type", "application/json")
	return r
}

// WithMultipartForm constructs a multipart/form-data body from formFields and fileFields.
// If any error occurs (e.g. file not found), it is stored in the Request.
func (r *Request) WithMultipartForm(formFields map[string]string, fileFields map[string]string) *Request {
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)

	// Add form fields.
	for key, val := range formFields {
		if err := writer.WriteField(key, val); err != nil {
			r.buildErr = err
			return r
		}
	}

	for field, filePath := range fileFields {
		if err := func() (retErr error) {
			file, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer func() {
				if closeError := file.Close(); closeError != nil && retErr == nil {
					retErr = closeError
				}
			}()

			part, err := writer.CreateFormFile(field, filepath.Base(filePath))
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, file); err != nil {
				return err
			}
			return nil
		}(); err != nil {
			r.buildErr = err
			return r
		}
	}

	if err := writer.Close(); err != nil {
		r.buildErr = err
		return r
	}

	r.body = &b
	r.isMultipart = true
	r.WithHeader("Content-Type", writer.FormDataContentType())
	return r
}

// BuildHTTPRequest constructs an *http.Request from the Request.
// It returns an error if any issue occurred during building (e.g. invalid URL or previous build error).
func (r *Request) BuildHTTPRequest() (*http.Request, error) {
	if r.buildErr != nil {
		return nil, r.buildErr
	}
	if r.url == "" {
		return nil, errors.New("request URL is empty")
	}
	parsedURL, err := url.Parse(r.url)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	q := parsedURL.Query()
	for key, values := range r.queryParams {
		for _, v := range values {
			q.Add(key, v)
		}
	}
	parsedURL.RawQuery = q.Encode()

	httpReq, err := http.NewRequest(r.method, parsedURL.String(), r.body)
	if err != nil {
		return nil, err
	}
	for key, value := range r.headers {
		httpReq.Header.Set(key, value)
	}
	return httpReq, nil
}

// Response wraps a http.Response to provide helper methods.
type Response struct {
	*http.Response
}

// Close closes the response body.
func (r *Response) Close() error {
	return r.Body.Close()
}

// JSON decodes the JSON response into the provided variable.
// It automatically closes the response body.
func (r *Response) JSON(v interface{}) (err error) {
	defer func() {
		if closeErr := r.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return json.NewDecoder(r.Body).Decode(v)
}

// Bytes reads the full response body into a byte slice.
// It automatically closes the response body.
func (r *Response) Bytes() (body []byte, err error) {
	defer func() {
		if closeErr := r.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return io.ReadAll(r.Body)
}

// SaveToFile writes the response body to a file at the given path.
// It automatically closes the response body.
func (r *Response) SaveToFile(filePath string) (err error) {
	defer func() {
		if closeErr := r.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(f, r.Body)
	return err
}

// StreamChunks reads the response body in chunks and passes each chunk to the callback.
// An optional buffer size can be provided (default is 4096 bytes). The response body is not automatically closed.
func (r *Response) StreamChunks(callback func(chunk []byte), bufSizes ...int) error {
	if len(bufSizes) > 1 {
		return fmt.Errorf("only one optional buffer size value is allowed, got %d", len(bufSizes))
	}

	bufSize := 4096
	if len(bufSizes) == 1 {
		if bufSizes[0] <= 0 {
			bufSize = 4096
		} else {
			bufSize = bufSizes[0]
		}
	}

	buf := make([]byte, bufSize)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			callback(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// AsyncResponse represents the eventual outcome of an asynchronous HTTP call.
type AsyncResponse struct {
	Response *Response
	Error    error
}
