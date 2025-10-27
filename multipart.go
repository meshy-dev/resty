package resty

import (
	"io"
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
