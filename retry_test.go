// Copyright (c) 2015-2024 Jeevanandam M (jeeva@myjeeva.com), All rights reserved.
// resty source code and usage is governed by a MIT style
// license that can be found in the LICENSE file.

package resty

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBackoffSuccess(t *testing.T) {
	attempts := 3
	externalCounter := 0
	retryErr := Backoff(func() (*Response, error) {
		externalCounter++
		if externalCounter < attempts {
			return nil, errors.New("not yet got the number we're after")
		}

		return nil, nil
	})

	assertError(t, retryErr)
	assertEqual(t, externalCounter, attempts)
}

func TestBackoffNoWaitForLastRetry(t *testing.T) {
	attempts := 1
	externalCounter := 0
	numRetries := 1

	canceledCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp := &Response{
		Request: &Request{
			ctx: canceledCtx,
			client: &Client{
				RetryAfter: func(*Client, *Response) (time.Duration, error) {
					return 6, nil
				},
			},
		},
	}

	retryErr := Backoff(func() (*Response, error) {
		externalCounter++
		return resp, nil
	}, RetryConditions([]RetryConditionFunc{func(response *Response, err error) bool {
		if externalCounter == attempts+numRetries {
			// Backoff returns context canceled if goes to sleep after last retry.
			cancel()
		}
		return true
	}}), Retries(numRetries))

	assertNil(t, retryErr)
}

func TestBackoffTenAttemptsSuccess(t *testing.T) {
	attempts := 10
	externalCounter := 0
	retryErr := Backoff(func() (*Response, error) {
		externalCounter++
		if externalCounter < attempts {
			return nil, errors.New("not yet got the number we're after")
		}
		return nil, nil
	}, Retries(attempts), WaitTime(5), MaxWaitTime(500))

	assertError(t, retryErr)
	assertEqual(t, externalCounter, attempts)
}

// Check to make sure the conditional of the retry condition is being used
func TestConditionalBackoffCondition(t *testing.T) {
	attempts := 3
	counter := 0
	check := RetryConditionFunc(func(*Response, error) bool {
		return attempts != counter
	})
	retryErr := Backoff(func() (*Response, error) {
		counter++
		return nil, nil
	}, RetryConditions([]RetryConditionFunc{check}))

	assertError(t, retryErr)
	assertEqual(t, counter, attempts)
}

// Check to make sure that if the conditional is false we don't retry
func TestConditionalBackoffConditionNonExecution(t *testing.T) {
	attempts := 3
	counter := 0

	retryErr := Backoff(func() (*Response, error) {
		counter++
		return nil, nil
	}, RetryConditions([]RetryConditionFunc{filler}))

	assertError(t, retryErr)
	assertNotEqual(t, counter, attempts)
}

// Check to make sure that RetryHooks are executed
func TestOnRetryBackoff(t *testing.T) {
	attempts := 3
	counter := 0

	hook := func(r *Response, err error) {
		counter++
	}

	retryErr := Backoff(func() (*Response, error) {
		return nil, nil
	}, RetryHooks([]OnRetryFunc{hook}))

	assertError(t, retryErr)
	assertNotEqual(t, counter, attempts)
}

// Check to make sure the functions added to add conditionals work
func TestConditionalGet(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()
	attemptCount := 1
	externalCounter := 0

	// This check should pass on first run, and let the response through
	check := RetryConditionFunc(func(*Response, error) bool {
		externalCounter++
		return attemptCount != externalCounter
	})

	client := dc().AddRetryCondition(check).SetRetryCount(1)
	resp, err := client.R().
		SetQueryParam("request_no", strconv.FormatInt(time.Now().Unix(), 10)).
		Get(ts.URL + "/")

	assertError(t, err)
	assertEqual(t, http.StatusOK, resp.StatusCode())
	assertEqual(t, "200 OK", resp.Status())
	assertNotNil(t, resp.Body())
	assertEqual(t, "TestGet: text response", resp.String())
	assertEqual(t, externalCounter, attemptCount)

	logResponse(t, resp)
}

// Check to make sure the package Function works.
func TestConditionalGetDefaultClient(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()
	attemptCount := 1
	externalCounter := 0

	// This check should pass on first run, and let the response through
	check := RetryConditionFunc(func(*Response, error) bool {
		externalCounter++
		return attemptCount != externalCounter
	})

	// Clear the default client.
	client := dc()
	// Proceed to check.
	client.AddRetryCondition(check).SetRetryCount(1)
	resp, err := client.R().
		SetQueryParam("request_no", strconv.FormatInt(time.Now().Unix(), 10)).
		Get(ts.URL + "/")

	assertError(t, err)
	assertEqual(t, http.StatusOK, resp.StatusCode())
	assertEqual(t, "200 OK", resp.Status())
	assertNotNil(t, resp.Body())
	assertEqual(t, "TestGet: text response", resp.String())
	assertEqual(t, externalCounter, attemptCount)

	logResponse(t, resp)
}

func TestClientRetryGet(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	c := dc().
		SetTimeout(time.Second * 3).
		SetRetryCount(3)

	resp, err := c.R().Get(ts.URL + "/set-retrycount-test")
	assertEqual(t, "", resp.Status())
	assertEqual(t, "", resp.Proto())
	assertEqual(t, 0, resp.StatusCode())
	assertEqual(t, 0, len(resp.Cookies()))
	assertNotNil(t, resp.Body())
	assertEqual(t, 0, len(resp.Header()))

	assertEqual(t, true, strings.HasPrefix(err.Error(), "Get "+ts.URL+"/set-retrycount-test") ||
		strings.HasPrefix(err.Error(), "Get \""+ts.URL+"/set-retrycount-test\""))
}

func TestClientRetryWait(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := time.Duration(50) * time.Millisecond
	retryMaxWaitTime := time.Duration(150) * time.Millisecond

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)
	_, _ = c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 6 attempts were made
	assertEqual(t, attempt, 6)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	for i := 1; i < len(retryIntervals); i++ {
		slept := time.Duration(retryIntervals[i])
		// Ensure that client has slept some duration between
		// waitTime and maxWaitTime for consequent requests
		isExceed := (slept - retryMaxWaitTime) > 100*time.Millisecond // avoid flaky test due to time measurement precision
		if slept < retryWaitTime || isExceed {
			t.Errorf("Client has slept %f seconds before retry %d", slept.Seconds(), i)
		}
	}
}

func TestClientRetryWaitMaxInfinite(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := time.Duration(100) * time.Millisecond
	retryMaxWaitTime := time.Duration(-1.0) // negative value

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)
	_, _ = c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 6 attempts were made
	assertEqual(t, attempt, 6)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	for i := 1; i < len(retryIntervals); i++ {
		slept := time.Duration(retryIntervals[i])
		// Ensure that client has slept some duration between
		// waitTime and maxWaitTime for consequent requests
		if slept < retryWaitTime {
			t.Errorf("Client has slept %f seconds before retry %d", slept.Seconds(), i)
		}
	}
}

func TestClientRetryWaitMaxMinimum(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	const retryMaxWaitTime = time.Nanosecond // minimal duration value

	c := dc().
		SetRetryCount(1).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		AddRetryCondition(func(*Response, error) bool { return true })
	_, err := c.R().Get(ts.URL + "/set-retrywaittime-test")
	assertError(t, err)
}

func TestClientRetryWaitCallbackError(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := 50 * time.Millisecond
	retryMaxWaitTime := 150 * time.Millisecond

	retryAfter := func(client *Client, resp *Response) (time.Duration, error) {
		return 0, errors.New("quota exceeded")
	}

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		SetRetryAfter(retryAfter).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)

	_, err := c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 1 attempts were made
	assertEqual(t, attempt, 1)

	// non-nil error was returned
	assertNotEqual(t, nil, err)
}

func TestClientRetryWaitCallback(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := 50 * time.Millisecond
	retryMaxWaitTime := 150 * time.Millisecond

	retryAfter := func(client *Client, resp *Response) (time.Duration, error) {
		return 50 * time.Millisecond, nil
	}

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		SetRetryAfter(retryAfter).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)
	_, _ = c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 6 attempts were made
	assertEqual(t, attempt, 6)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	for i := 1; i < len(retryIntervals); i++ {
		slept := time.Duration(retryIntervals[i])
		// Ensure that client has slept some duration between
		// waitTime and maxWaitTime for consequent requests
		if slept < 5*time.Second-5*time.Millisecond || 5*time.Second+5*time.Millisecond < slept {
			t.Logf("Client has slept %f seconds before retry %d", slept.Seconds(), i)
		}
	}
}

func TestClientRetryWaitCallbackTooShort(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := 50 * time.Millisecond
	retryMaxWaitTime := 150 * time.Millisecond

	retryAfter := func(client *Client, resp *Response) (time.Duration, error) {
		return 10 * time.Millisecond, nil // too short duration
	}

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		SetRetryAfter(retryAfter).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)
	_, _ = c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 6 attempts were made
	assertEqual(t, attempt, 6)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	for i := 1; i < len(retryIntervals); i++ {
		slept := time.Duration(retryIntervals[i])
		// Ensure that client has slept some duration between
		// waitTime and maxWaitTime for consequent requests
		if slept < retryWaitTime-5*time.Millisecond || retryWaitTime+5*time.Millisecond < slept {
			t.Logf("Client has slept %f seconds before retry %d", slept.Seconds(), i)
		}
	}
}

func TestClientRetryWaitCallbackTooLong(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := 10 * time.Millisecond
	retryMaxWaitTime := 100 * time.Millisecond

	retryAfter := func(client *Client, resp *Response) (time.Duration, error) {
		return 150 * time.Millisecond, nil // too long duration
	}

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		SetRetryAfter(retryAfter).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)
	_, _ = c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 6 attempts were made
	assertEqual(t, attempt, 6)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	for i := 1; i < len(retryIntervals); i++ {
		slept := time.Duration(retryIntervals[i])
		// Ensure that client has slept some duration between
		// waitTime and maxWaitTime for consequent requests
		if slept < retryMaxWaitTime-5*time.Millisecond || retryMaxWaitTime+5*time.Millisecond < slept {
			t.Logf("Client has slept %f seconds before retry %d", slept.Seconds(), i)
		}
	}
}

func TestClientRetryWaitCallbackSwitchToDefault(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := 1 * time.Second
	retryMaxWaitTime := 3 * time.Second

	retryAfter := func(client *Client, resp *Response) (time.Duration, error) {
		return 0, nil // use default algorithm to determine retry-after time
	}

	c := dc().
		EnableTrace().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		SetRetryAfter(retryAfter).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)
	resp, _ := c.R().Get(ts.URL + "/set-retrywaittime-test")

	// 6 attempts were made
	assertEqual(t, attempt, 6)
	assertEqual(t, resp.Request.Attempt, 6)
	assertEqual(t, resp.Request.TraceInfo().RequestAttempt, 6)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	for i := 1; i < len(retryIntervals); i++ {
		slept := time.Duration(retryIntervals[i])
		expected := (1 << (uint(i - 1))) * time.Second
		if expected > retryMaxWaitTime {
			expected = retryMaxWaitTime
		}

		// Ensure that client has slept some duration between
		// waitTime and maxWaitTime for consequent requests
		if slept < expected/2-5*time.Millisecond || expected+5*time.Millisecond < slept {
			t.Errorf("Client has slept %f seconds before retry %d", slept.Seconds(), i)
		}
	}
}

func TestClientRetryCancel(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	retryCount := 5
	retryIntervals := make([]uint64, retryCount+1)

	// Set retry wait times that do not intersect with default ones
	retryWaitTime := time.Duration(10) * time.Second
	retryMaxWaitTime := time.Duration(20) * time.Second

	c := dc().
		SetRetryCount(retryCount).
		SetRetryWaitTime(retryWaitTime).
		SetRetryMaxWaitTime(retryMaxWaitTime).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				timeSlept, _ := strconv.ParseUint(string(r.Body()), 10, 64)
				retryIntervals[attempt] = timeSlept
				attempt++
				return true
			},
		)

	timeout := 2 * time.Second

	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	_, _ = c.R().SetContext(ctx).Get(ts.URL + "/set-retrywaittime-test")

	// 1 attempts were made
	assertEqual(t, attempt, 1)

	// Initial attempt has 0 time slept since last request
	assertEqual(t, retryIntervals[0], uint64(0))

	// Second attempt should be interrupted on context timeout
	if time.Duration(retryIntervals[1]) > timeout {
		t.Errorf("Client didn't awake on context cancel")
	}
	cancelFunc()
}

func TestClientRetryPost(t *testing.T) {
	ts := createPostServer(t)
	defer ts.Close()

	usersmap := map[string]interface{}{
		"user1": map[string]interface{}{"FirstName": "firstname1", "LastName": "lastname1", "ZipCode": "10001"},
	}

	var users []map[string]interface{}
	users = append(users, usersmap)

	c := dc()
	c.SetRetryCount(3)
	c.AddRetryCondition(RetryConditionFunc(func(r *Response, _ error) bool {
		return r.StatusCode() >= http.StatusInternalServerError
	}))

	resp, _ := c.R().
		SetBody(&users).
		Post(ts.URL + "/usersmap?status=500")

	if resp != nil {
		if resp.StatusCode() == http.StatusInternalServerError {
			t.Logf("Got response body: %s", resp.String())
			var usersResponse []map[string]interface{}
			err := json.Unmarshal(resp.body, &usersResponse)
			assertError(t, err)

			if !reflect.DeepEqual(users, usersResponse) {
				t.Errorf("Expected request body to be echoed back as response body. Instead got: %s", resp.String())
			}

			return
		}
		t.Errorf("Got unexpected response code: %d with body: %s", resp.StatusCode(), resp.String())
	}
}

func TestClientRetryErrorRecover(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	c := dc().
		SetRetryCount(2).
		SetError(AuthError{}).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				err, ok := r.Error().(*AuthError)
				retry := ok && r.StatusCode() == 429 && err.Message == "too many"
				return retry
			},
		)

	resp, err := c.R().
		SetHeader(hdrContentTypeKey, "application/json; charset=utf-8").
		SetJSONEscapeHTML(false).
		SetResult(AuthSuccess{}).
		Get(ts.URL + "/set-retry-error-recover")

	assertError(t, err)

	authSuccess := resp.Result().(*AuthSuccess)

	assertEqual(t, http.StatusOK, resp.StatusCode())
	assertEqual(t, "hello", authSuccess.Message)

	assertNil(t, resp.Error())
}

func TestClientRetryCount(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	c := dc().
		SetTimeout(time.Second * 3).
		SetRetryCount(1).
		AddRetryCondition(
			func(r *Response, _ error) bool {
				attempt++
				return true
			},
		)

	resp, err := c.R().Get(ts.URL + "/set-retrycount-test")
	assertEqual(t, "", resp.Status())
	assertEqual(t, "", resp.Proto())
	assertEqual(t, 0, resp.StatusCode())
	assertEqual(t, 0, len(resp.Cookies()))
	assertNotNil(t, resp.Body())
	assertEqual(t, 0, len(resp.Header()))

	// 2 attempts were made
	assertEqual(t, attempt, 2)

	assertEqual(t, true, strings.HasPrefix(err.Error(), "Get "+ts.URL+"/set-retrycount-test") ||
		strings.HasPrefix(err.Error(), "Get \""+ts.URL+"/set-retrycount-test\""))
}

func TestClientErrorRetry(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	c := dc().
		SetTimeout(time.Second * 3).
		SetRetryCount(1).
		AddRetryAfterErrorCondition()

	resp, err := c.R().
		SetHeader(hdrContentTypeKey, "application/json; charset=utf-8").
		SetJSONEscapeHTML(false).
		SetResult(AuthSuccess{}).
		Get(ts.URL + "/set-retry-error-recover")

	assertError(t, err)

	authSuccess := resp.Result().(*AuthSuccess)

	assertEqual(t, http.StatusOK, resp.StatusCode())
	assertEqual(t, "hello", authSuccess.Message)

	assertNil(t, resp.Error())
}

func TestClientRetryHook(t *testing.T) {
	ts := createGetServer(t)
	defer ts.Close()

	attempt := 0

	c := dc().
		SetRetryCount(2).
		SetTimeout(time.Second * 3).
		AddRetryHook(
			func(r *Response, _ error) {
				attempt++
			},
		)

	resp, err := c.R().Get(ts.URL + "/set-retrycount-test")
	assertEqual(t, "", resp.Status())
	assertEqual(t, "", resp.Proto())
	assertEqual(t, 0, resp.StatusCode())
	assertEqual(t, 0, len(resp.Cookies()))
	assertNotNil(t, resp.Body())
	assertEqual(t, 0, len(resp.Header()))

	assertEqual(t, 3, attempt)

	assertEqual(t, true, strings.HasPrefix(err.Error(), "Get "+ts.URL+"/set-retrycount-test") ||
		strings.HasPrefix(err.Error(), "Get \""+ts.URL+"/set-retrycount-test\""))
}

func filler(*Response, error) bool {
	return false
}

var errSeekFailure = fmt.Errorf("failing seek test")

type failingSeeker struct {
	reader *bytes.Reader
}

func (f failingSeeker) Read(b []byte) (n int, err error) {
	return f.reader.Read(b)
}

func (f failingSeeker) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		return 0, errSeekFailure
	}

	return f.reader.Seek(offset, whence)
}

func TestResetMultipartReaderSeekStartError(t *testing.T) {
	ts := createFilePostServer(t)
	defer ts.Close()

	testSeeker := &failingSeeker{
		bytes.NewReader([]byte("test")),
	}

	c := dc().
		SetRetryCount(2).
		SetTimeout(time.Second * 3).
		AddRetryAfterErrorCondition()

	resp, err := c.R().
		SetFileReader("name", "filename", testSeeker).
		Post(ts.URL + "/set-reset-multipart-readers-test")

	assertEqual(t, 500, resp.StatusCode())
	assertEqual(t, err.Error(), errSeekFailure.Error())
}

func TestResetMultipartReaders(t *testing.T) {
	ts := createFilePostServer(t)
	defer ts.Close()

	str := "test"
	buf := []byte(str)

	bufReader := bytes.NewReader(buf)
	bufCpy := make([]byte, len(buf))

	c := dc().
		SetRetryCount(2).
		SetTimeout(time.Second * 3).
		AddRetryAfterErrorCondition().
		AddRetryHook(
			func(response *Response, _ error) {
				read, err := bufReader.Read(bufCpy)

				assertNil(t, err)
				assertEqual(t, len(buf), read)
				assertEqual(t, str, string(bufCpy))
			},
		)

	resp, err := c.R().
		SetFileReader("name", "filename", bufReader).
		Post(ts.URL + "/set-reset-multipart-readers-test")

	assertEqual(t, 500, resp.StatusCode())
	assertNil(t, err)
}

func TestResetMultipartFieldReaderSeekStartError(t *testing.T) {
	ts := createFilePostServer(t)
	defer ts.Close()

	testSeeker := &failingSeeker{
		bytes.NewReader([]byte("test")),
	}

	c := dc().
		SetRetryCount(2).
		SetTimeout(time.Second * 3).
		AddRetryAfterErrorCondition()

	resp, err := c.R().
		SetMultipartField("file", "audio.wav", "audio/wav", testSeeker).
		Post(ts.URL + "/set-reset-multipart-readers-test")

	assertEqual(t, 500, resp.StatusCode())
	assertEqual(t, err.Error(), errSeekFailure.Error())
}

func TestResetMultipartFieldReaders(t *testing.T) {
	ts := createFilePostServer(t)
	defer ts.Close()

	str := "test"
	buf := []byte(str)

	bufReader := bytes.NewReader(buf)
	bufCpy := make([]byte, len(buf))

	c := dc().
		SetRetryCount(2).
		SetTimeout(time.Second * 3).
		AddRetryAfterErrorCondition().
		AddRetryHook(
			func(response *Response, _ error) {
				read, err := bufReader.Read(bufCpy)

				assertNil(t, err)
				assertEqual(t, len(buf), read)
				assertEqual(t, str, string(bufCpy))
			},
		)

	resp, err := c.R().
		SetMultipartField("file", "audio.wav", "audio/wav", bufReader).
		Post(ts.URL + "/set-reset-multipart-readers-test")

	assertEqual(t, 500, resp.StatusCode())
	assertNil(t, err)
}

// opaqueSectionable hides the concrete reader type so snapshotReader cannot
// match it, while still exposing ReaderAt+Seeker — forcing the per-attempt
// SectionReader path (the same shape as *os.File).
type opaqueSectionable struct{ *bytes.Reader }

// opaqueReadSeeker exposes only io.ReadSeeker (no ReaderAt): not replayable
// per attempt, so it must be rejected when retries are enabled.
type opaqueReadSeeker struct{ io.ReadSeeker }

// opaqueReader is a plain, non-seekable reader: likewise rejected with
// retries enabled.
type opaqueReader struct{ io.Reader }

// TestRetryBodyNotCorruptedByInFlightUpload reproduces the shared-body-reader
// retry race: the server answers 503 before the upload finishes and then keeps
// draining slowly, so the transport's background body write is still reading
// when the retry fires (net/http keeps writing the request body after an early
// response, and with DoNotParseResponse nothing closes the discarded response
// to tear that connection down). Each attempt must therefore get its own
// stateless view of the body — a shared seeker cursor truncates and shifts the
// retried request.
func TestRetryBodyNotCorruptedByInFlightUpload(t *testing.T) {
	const payloadSize = 64 << 20
	payload := make([]byte, payloadSize)
	for off := 0; off < payloadSize; off += 8 {
		binary.BigEndian.PutUint64(payload[off:], uint64(off))
	}

	tests := map[string]struct {
		body interface{}
		// The SectionReader path is a type NewRequest cannot size, so it goes
		// out chunked; http.ReadRequest reports that as ContentLength -1.
		wantChunked bool
	}{
		"bytes body":  {body: payload},
		"reader body": {body: bytes.NewReader(payload)},
		"buffer body": {body: bytes.NewBuffer(payload)},
		"sectionable body": {
			body:        &opaqueSectionable{bytes.NewReader(payload)},
			wantChunked: true,
		},
		"section reader body": {
			body:        io.NewSectionReader(bytes.NewReader(payload), 0, payloadSize),
			wantChunked: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			assertNil(t, err)
			defer ln.Close()

			type uploadResult struct {
				contentLength int64
				body          []byte
				readErr       error
			}
			results := make(chan uploadResult, 1)
			go func() {
				for connIdx := 1; ; connIdx++ {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					go func(conn net.Conn, connIdx int) {
						defer conn.Close()
						_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
						req, err := http.ReadRequest(bufio.NewReader(conn))
						if err != nil {
							return
						}
						if connIdx == 1 {
							// Early 503 with a body nobody will read, then drain the
							// upload slowly to hold the client's background body write
							// alive across the retry wait.
							_, _ = io.WriteString(conn, "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 5\r\n\r\nbusy!")
							buf := make([]byte, 32<<10)
							for {
								time.Sleep(5 * time.Millisecond)
								if _, err := req.Body.Read(buf); err != nil {
									return
								}
							}
						}
						body, readErr := io.ReadAll(req.Body)
						_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
						results <- uploadResult{contentLength: req.ContentLength, body: body, readErr: readErr}
					}(conn, connIdx)
				}
			}()

			c := NewWithClient(&http.Client{}).
				SetBaseURL("http://" + ln.Addr().String()).
				SetRetryCount(1).
				SetRetryWaitTime(100 * time.Millisecond).
				SetRetryMaxWaitTime(100 * time.Millisecond).
				SetCloseConnection(true).
				AddRetryCondition(func(r *Response, err error) bool {
					return err == nil && r != nil && r.StatusCode() == http.StatusServiceUnavailable
				})

			resp, err := c.R().SetDoNotParseResponse(true).SetBody(tt.body).Post("/upload")
			assertNil(t, err)
			assertNotNil(t, resp)
			if body := resp.RawBody(); body != nil {
				_ = body.Close()
			}

			got := <-results
			assertNil(t, got.readErr)
			if tt.wantChunked {
				assertEqual(t, int64(-1), got.contentLength)
			} else {
				assertEqual(t, int64(payloadSize), got.contentLength)
			}
			assertEqual(t, payloadSize, len(got.body))
			if !bytes.Equal(payload, got.body) {
				for off := 0; off+8 <= len(got.body); off += 8 {
					if v := binary.BigEndian.Uint64(got.body[off:]); v != uint64(off) {
						t.Fatalf("retried body corrupted: word at offset %d encodes offset %d (shift %+d)",
							off, v, int64(v)-int64(off))
					}
				}
				t.Fatal("retried body corrupted")
			}
		})
	}
}

// TestRetryNonReplayableBodyRejected ensures a body that cannot be replayed
// per attempt fails fast when retries are enabled, instead of silently
// corrupting the retried request — and still streams when retries are off.
func TestRetryNonReplayableBodyRejected(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer svr.Close()

	bodies := map[string]func() interface{}{
		"plain reader":       func() interface{} { return &opaqueReader{strings.NewReader("hello")} },
		"seeker sans readat": func() interface{} { return &opaqueReadSeeker{strings.NewReader("hello")} },
	}

	for name, makeBody := range bodies {
		t.Run(name, func(t *testing.T) {
			retrying := NewWithClient(&http.Client{}).SetRetryCount(1)
			_, err := retrying.R().SetBody(makeBody()).Post(svr.URL)
			assertNotNil(t, err)
			assertEqual(t, true, errors.Is(err, errRetryUnsupportedBody))

			plain := NewWithClient(&http.Client{})
			resp, err := plain.R().SetBody(makeBody()).Post(svr.URL)
			assertNil(t, err)
			assertEqual(t, http.StatusOK, resp.StatusCode())
		})
	}
}

// TestRetryExhaustedResponseBodyReadable ensures only discarded attempts'
// responses are closed on retry: when retries are exhausted, the final
// response body must stay open so a DoNotParseResponse caller can still read
// the error payload.
func TestRetryExhaustedResponseBodyReadable(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("busy!"))
	}))
	defer svr.Close()

	c := NewWithClient(&http.Client{}).
		SetRetryCount(2).
		SetRetryWaitTime(10 * time.Millisecond).
		SetRetryMaxWaitTime(10 * time.Millisecond).
		AddRetryCondition(func(r *Response, err error) bool {
			return err == nil && r != nil && r.StatusCode() == http.StatusServiceUnavailable
		})

	resp, err := c.R().SetDoNotParseResponse(true).SetBody([]byte("hello")).Post(svr.URL)
	assertNil(t, err)
	assertNotNil(t, resp)
	assertEqual(t, http.StatusServiceUnavailable, resp.StatusCode())

	body := resp.RawBody()
	assertNotNil(t, body)
	data, err := io.ReadAll(body)
	assertNil(t, err)
	_ = body.Close()
	assertEqual(t, "busy!", string(data))
}
