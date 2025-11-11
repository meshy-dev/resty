// Copyright (c) 2015-2024 Jeevanandam M (jeeva@myjeeva.com), All rights reserved.
// resty source code and usage is governed by a MIT style
// license that can be found in the LICENSE file.

package resty

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/textproto"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

// ‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Logger interface
// _______________________________________________________________________

// Logger interface is to abstract the logging from Resty. Gives control to
// the Resty users, choice of the logger.
type Logger interface {
	Errorf(format string, v ...interface{})
	Warnf(format string, v ...interface{})
	Debugf(format string, v ...interface{})
}

func createLogger() *logger {
	l := &logger{l: log.New(os.Stderr, "", log.Ldate|log.Lmicroseconds)}
	return l
}

var _ Logger = (*logger)(nil)

type logger struct {
	l *log.Logger
}

func (l *logger) Errorf(format string, v ...interface{}) {
	l.output("ERROR RESTY "+format, v...)
}

func (l *logger) Warnf(format string, v ...interface{}) {
	l.output("WARN RESTY "+format, v...)
}

func (l *logger) Debugf(format string, v ...interface{}) {
	l.output("DEBUG RESTY "+format, v...)
}

func (l *logger) output(format string, v ...interface{}) {
	if len(v) == 0 {
		l.l.Print(format)
		return
	}
	l.l.Printf(format, v...)
}

// ‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Rate Limiter interface
// _______________________________________________________________________

type RateLimiter interface {
	Allow() bool
}

var ErrRateLimitExceeded = errors.New("rate limit exceeded")

// ‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// Package Helper methods
// _______________________________________________________________________

// IsStringEmpty method tells whether given string is empty or not
func IsStringEmpty(str string) bool {
	return len(strings.TrimSpace(str)) == 0
}

// DetectContentType method is used to figure out `Request.Body` content type for request header
func DetectContentType(body interface{}) string {
	contentType := plainTextType
	kind := kindOf(body)
	switch kind {
	case reflect.Struct, reflect.Map:
		contentType = jsonContentType
	case reflect.String:
		contentType = plainTextType
	default:
		if b, ok := body.([]byte); ok {
			contentType = http.DetectContentType(b)
		} else if kind == reflect.Slice {
			contentType = jsonContentType
		}
	}

	return contentType
}

// IsJSONType method is to check JSON content type or not
func IsJSONType(ct string) bool {
	return jsonCheck.MatchString(ct)
}

// IsXMLType method is to check XML content type or not
func IsXMLType(ct string) bool {
	return xmlCheck.MatchString(ct)
}

// Unmarshalc content into object from JSON or XML
func Unmarshalc(c *Client, ct string, b []byte, d interface{}) (err error) {
	if IsJSONType(ct) {
		err = c.JSONUnmarshal(b, d)
	} else if IsXMLType(ct) {
		err = c.XMLUnmarshal(b, d)
	}

	return
}

// ‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾‾
// RequestLog and ResponseLog type
// _______________________________________________________________________

// RequestLog struct is used to collected information from resty request
// instance for debug logging. It sent to request log callback before resty
// actually logs the information.
type RequestLog struct {
	Header http.Header
	Body   string
}

// ResponseLog struct is used to collected information from resty response
// instance for debug logging. It sent to response log callback before resty
// actually logs the information.
type ResponseLog struct {
	Header http.Header
	Body   string
}

// way to disable the HTML escape as opt-in
func jsonMarshal(c *Client, r *Request) error {
	if !r.jsonEscapeHTML || !c.jsonEscapeHTML {
		var buf bytes.Buffer
		err := noescapeJSONMarshal(&buf, r.Body)
		if err != nil {
			return err
		}
		r.bodyReadSeeker = bytes.NewReader(buf.Bytes())
		return nil
	}

	data, err := c.JSONMarshal(r.Body)
	if err != nil {
		return err
	}
	r.bodyReadSeeker = bytes.NewReader(data)
	return nil
}

func xmlMarshal(c *Client, r *Request) error {
	data, err := c.XMLMarshal(r.Body)
	if err != nil {
		return err
	}
	r.bodyReadSeeker = bytes.NewReader(data)
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if !IsStringEmpty(s) {
			return s
		}
	}
	return ""
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func createMultipartHeader(param, fileName, contentType string) textproto.MIMEHeader {
	hdr := make(textproto.MIMEHeader)

	var contentDispositionValue string
	if IsStringEmpty(fileName) {
		contentDispositionValue = fmt.Sprintf(`form-data; name="%s"`, param)
	} else {
		contentDispositionValue = fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
			param, escapeQuotes(fileName))
	}
	hdr.Set("Content-Disposition", contentDispositionValue)

	if !IsStringEmpty(contentType) {
		hdr.Set(hdrContentTypeKey, contentType)
	}
	return hdr
}

func closeFieldReaders(fields []*MultipartField) {
	for _, field := range fields {
		closeq(field.Reader)
	}
}

func getPointer(v interface{}) interface{} {
	vv := valueOf(v)
	if vv.Kind() == reflect.Ptr {
		return v
	}
	return reflect.New(vv.Type()).Interface()
}

func isPayloadSupported(m string, allowMethodGet bool) bool {
	return !(m == MethodHead || m == MethodOptions || (m == MethodGet && !allowMethodGet))
}

func typeOf(i interface{}) reflect.Type {
	return indirect(valueOf(i)).Type()
}

func valueOf(i interface{}) reflect.Value {
	return reflect.ValueOf(i)
}

func indirect(v reflect.Value) reflect.Value {
	return reflect.Indirect(v)
}

func kindOf(v interface{}) reflect.Kind {
	return typeOf(v).Kind()
}

func createDirectory(dir string) (err error) {
	if _, err = os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			if err = os.MkdirAll(dir, 0755); err != nil {
				return
			}
		}
	}
	return
}

func canJSONMarshal(contentType string, kind reflect.Kind) bool {
	return IsJSONType(contentType) && (kind == reflect.Struct || kind == reflect.Map || kind == reflect.Slice)
}

func functionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

// MaxBufferSize is the maximum size of buffer to be kept in pool.
// Buffers larger than this size will not be put back to pool.
// Defaults to 10 MiB.
// Users can change this value as per requirement.
var MaxBufferSize = 10 * 1024 * 1024 // 10 MiB

func acquireBuffer() *bytes.Buffer {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func releaseBuffer(buf *bytes.Buffer) {
	// Put back the buffer to pool only if its capacity is within limit.
	// See also https://github.com/bytedance/sonic/issues/614
	if buf != nil && buf.Cap() <= MaxBufferSize {
		bufPool.Put(buf)
	}
}

func closeq(v interface{}) {
	if c, ok := v.(io.Closer); ok {
		silently(c.Close())
	}
}

func silently(_ ...interface{}) {}

func composeHeaders(c *Client, r *Request, hdrs http.Header) string {
	str := make([]string, 0, len(hdrs))
	for _, k := range sortHeaderKeys(hdrs) {
		str = append(str, "\t"+strings.TrimSpace(fmt.Sprintf("%25s: %s", k, strings.Join(hdrs[k], ", "))))
	}
	return strings.Join(str, "\n")
}

func sortHeaderKeys(hdrs http.Header) []string {
	keys := make([]string, 0, len(hdrs))
	for key := range hdrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyHeaders(hdrs http.Header) http.Header {
	nh := http.Header{}
	for k, v := range hdrs {
		nh[k] = v
	}
	return nh
}

func wrapErrors(n error, inner error) error {
	if inner == nil {
		return n
	}
	if n == nil {
		return inner
	}
	return &restyError{
		err:   n,
		inner: inner,
	}
}

type restyError struct {
	err   error
	inner error
}

func (e *restyError) Error() string {
	return e.err.Error()
}

func (e *restyError) Unwrap() error {
	return e.inner
}

type noRetryErr struct {
	err error
}

func (e *noRetryErr) Error() string {
	return e.err.Error()
}

func wrapNoRetryErr(err error) error {
	if err != nil {
		err = &noRetryErr{err: err}
	}
	return err
}

func unwrapNoRetryErr(err error) error {
	if e, ok := err.(*noRetryErr); ok {
		err = e.err
	}
	return err
}
