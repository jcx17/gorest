package gorest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorest/gorest"
)

var _ = Describe("Request", func() {
	It("should set headers correctly", func() {
		req := gorest.NewRequest("GET", "http://example.com")
		req.WithHeader("X-Test", "value")
		req.WithHeaders(map[string]string{"X-Test-2": "value2"})
		httpReq, err := req.BuildHTTPRequest()
		Expect(err).NotTo(HaveOccurred())
		Expect(httpReq.Header.Get("X-Test")).To(Equal("value"))
		Expect(httpReq.Header.Get("X-Test-2")).To(Equal("value2"))
	})

	It("should add query parameters", func() {
		req := gorest.NewRequest("GET", "http://example.com")
		req.WithQueryParam("foo", "bar")
		httpReq, err := req.BuildHTTPRequest()
		Expect(err).NotTo(HaveOccurred())
		Expect(httpReq.URL.RawQuery).To(ContainSubstring("foo=bar"))
	})

	It("should set the body correctly with WithBody", func() {
		data := []byte("hello")
		req := gorest.NewRequest("POST", "http://example.com")
		req.WithBody(data)
		httpReq, err := req.BuildHTTPRequest()
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(httpReq.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("hello"))
	})

	It("should set JSON body and content-type header", func() {
		payload := map[string]string{"key": "value"}
		req := gorest.NewRequest("POST", "http://example.com")
		req.WithJSONBody(payload)
		httpReq, err := req.BuildHTTPRequest()
		Expect(err).NotTo(HaveOccurred())
		Expect(httpReq.Header.Get("Content-Type")).To(Equal("application/json"))
		body, err := io.ReadAll(httpReq.Body)
		Expect(err).NotTo(HaveOccurred())
		var parsed map[string]string
		err = json.Unmarshal(body, &parsed)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(payload))
	})

	It("should return an error for empty URL", func() {
		req := gorest.NewRequest("GET", "")
		_, err := req.BuildHTTPRequest()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("request URL is empty"))
	})

	It("should return an error for an invalid URL", func() {
		req := gorest.NewRequest("GET", "http://%41:8080/")
		_, err := req.BuildHTTPRequest()
		Expect(err).To(HaveOccurred())
	})

	Context("Multipart Form", func() {
		var (
			tmpFile  *os.File
			filePath string
		)

		BeforeEach(func() {
			var err error
			tmpFile, err = os.CreateTemp("", "testfile")
			Expect(err).NotTo(HaveOccurred())
			filePath = tmpFile.Name()
			_, err = tmpFile.WriteString("file content")
			Expect(err).NotTo(HaveOccurred())
			tmpFile.Close()
		})

		AfterEach(func() {
			os.Remove(filePath)
		})

		It("should build a multipart form request", func() {
			formFields := map[string]string{"field1": "value1"}
			fileFields := map[string]string{"file1": filePath}
			req := gorest.NewRequest("POST", "http://example.com")
			req.WithMultipartForm(formFields, fileFields)
			httpReq, err := req.BuildHTTPRequest()
			Expect(err).NotTo(HaveOccurred())
			// Check that Content-Type is multipart/form-data with a boundary.
			contentType := httpReq.Header.Get("Content-Type")
			Expect(contentType).To(ContainSubstring("multipart/form-data"))
			body, err := io.ReadAll(httpReq.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("value1"))
			Expect(string(body)).To(ContainSubstring("file content"))
		})

		It("should error if a file does not exist", func() {
			formFields := map[string]string{"field1": "value1"}
			fileFields := map[string]string{"file1": "nonexistent_file.txt"}
			req := gorest.NewRequest("POST", "http://example.com")
			req.WithMultipartForm(formFields, fileFields)
			_, err := req.BuildHTTPRequest()
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("Response", func() {
	It("should read JSON correctly", func() {
		jsonStr := `{"message": "hello"}`
		res := &http.Response{
			Status:     "200 OK",
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(jsonStr)),
		}
		response := &gorest.Response{Response: res}
		var result map[string]string
		err := response.JSON(&result)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(map[string]string{"message": "hello"}))
	})

	It("should read bytes correctly", func() {
		text := "some data"
		res := &http.Response{
			Status:     "200 OK",
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(text)),
		}
		response := &gorest.Response{Response: res}
		b, err := response.Bytes()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(text))
	})

	It("should save to file correctly", func() {
		text := "file data"
		res := &http.Response{
			Status:     "200 OK",
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(text)),
		}
		response := &gorest.Response{Response: res}
		tmpFile, err := os.CreateTemp("", "resp_test")
		Expect(err).NotTo(HaveOccurred())
		filePath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(filePath)
		err = response.SaveToFile(filePath)
		Expect(err).NotTo(HaveOccurred())
		data, err := os.ReadFile(filePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(text))
	})

	It("should stream chunks correctly with optional buffer size", func() {
		// Use a small buffer size to force multiple chunks.
		text := "line1\nline2\nline3\n"
		res := &http.Response{
			Status:     "200 OK",
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(text)),
		}
		response := &gorest.Response{Response: res}
		var chunks []string
		err := response.StreamChunks(func(chunk []byte) {
			chunks = append(chunks, string(chunk))
		}, 6)
		Expect(err).NotTo(HaveOccurred())
		var trimmed []string
		for _, s := range chunks {
			trimmed = append(trimmed, strings.TrimSuffix(s, "\n"))
		}
		Expect(trimmed).To(Equal([]string{"line1", "line2", "line3"}))
	})
})
