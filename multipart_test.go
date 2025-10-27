package resty

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

func TestRetryMultiPartUpload(t *testing.T) {
	cnt := 0
	ts := createTestServer(func(w http.ResponseWriter, r *http.Request) {
		if cnt < 1 {
			t.Log("ask client to retry")
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			cnt++
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
	c := New().SetDebug(true).
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
