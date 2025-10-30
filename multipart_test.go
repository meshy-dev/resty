package resty

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

func TestRetryMultiPartUploadWithStreaming(t *testing.T) {
	cnt := 0
	ts := createTestServer(func(w http.ResponseWriter, r *http.Request) {
		if cnt < 1 {
			t.Log("ask client to retry")
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			cnt++
			return
		}

		val := r.Header.Get("Content-Length")
		if len(val) > 0 {
			t.Error("Content-Length header should not be set for multipart upload with streaming")
			http.Error(w, "invalid header", http.StatusBadRequest)
			return
		}

		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			buf, _ := io.ReadAll(part)
			t.Log(part.FileName(), part.FormName(), len(buf))
			_ = part.Close()
			if len(buf) == 0 {
				t.Errorf("file %s is empty", part.FileName())
			}
		}
	})
	defer ts.Close()

	basePath := getTestDataPath()
	c := New().
		SetRetryCount(3).
		AddRetryAfterErrorCondition().
		SetRetryResetReaders(true)
	resp, err := c.R().
		SetFile("file1", filepath.Join(basePath, "test-img.png")).
		SetFile("file2", filepath.Join(basePath, "text-file.txt")).
		Post(ts.URL)
	assertError(t, err)
	assertEqual(t, http.StatusOK, resp.StatusCode())
}

func TestMultiPartUploadWithoutStreaming(t *testing.T) {
	ts := createTestServer(func(w http.ResponseWriter, r *http.Request) {
		val := r.Header.Get("Content-Length")
		if len(val) == 0 {
			t.Error("Missing Content-Length header for multipart upload without streaming")
			http.Error(w, "invalid header", http.StatusBadRequest)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			buf, _ := io.ReadAll(part)
			t.Log(part.FileName(), part.FormName(), len(buf))
			_ = part.Close()
			if len(buf) == 0 {
				t.Errorf("file %s is empty", part.FileName())
			}
		}
	})
	defer ts.Close()

	basePath := getTestDataPath()
	c := dc()
	resp, err := c.R().
		SetFile("file1", filepath.Join(basePath, "test-img.png")).
		SetFile("file2", filepath.Join(basePath, "text-file.txt")).
		SetDisableMultiPartStreamUpload(true).
		Post(ts.URL)
	assertError(t, err)
	assertEqual(t, http.StatusOK, resp.StatusCode())
}
