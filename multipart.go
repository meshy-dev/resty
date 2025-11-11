package resty

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

// ‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// MultipartField struct
// _______________________________________________________________________

// MultipartField struct represents the custom data part for a multipart request
type MultipartField struct {
	Param       string
	FileName    string
	ContentType string
	FilePath    string
	// Reader is an input of [io.Reader] for multipart upload. It
	// is optional if you set the FilePath value
	io.Reader
}

func (mf *MultipartField) writeToMultipartWriter(w *multipart.Writer) error {
	if len(mf.FilePath) > 0 && mf.Reader == nil {
		fr, err := os.Open(mf.FilePath)
		if err != nil {
			return err
		}
		mf.Reader = fr
	}
	r := mf.Reader

	buf := make([]byte, 32*1024)
	size, err := r.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}

	if len(mf.ContentType) == 0 {
		mf.ContentType = http.DetectContentType(buf[:size])
	}

	partWriter, err := w.CreatePart(createMultipartHeader(mf.Param, mf.FileName, mf.ContentType))
	if err != nil {
		return err
	}

	if _, err = partWriter.Write(buf[:size]); err != nil {
		return err
	}

	_, err = io.CopyBuffer(partWriter, r, buf)
	return err
}

type multipartAndPipeWriter struct {
	mw *multipart.Writer
	pw *io.PipeWriter
}

func (m *multipartAndPipeWriter) Close() error {
	if err := m.mw.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}
	if err := m.pw.Close(); err != nil {
		return fmt.Errorf("close pipe writer: %w", err)
	}
	return nil
}
