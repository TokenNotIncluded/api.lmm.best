package common

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/gorilla/websocket"
)

var ErrLimitExceeded = errors.New("byte limit exceeded")

// LimitBuffer is an io.Writer whose retained memory never exceeds Limit.
// Once full it rejects the write without growing its backing slice.
type LimitBuffer struct {
	data  []byte
	limit int
}

func NewLimitBuffer(limit int) *LimitBuffer {
	if limit < 0 {
		limit = 0
	}
	return &LimitBuffer{limit: limit}
}

func (b *LimitBuffer) reserve(size int) ([]byte, error) {
	if b == nil || size < 0 {
		return nil, ErrLimitExceeded
	}
	start := len(b.data)
	// Check the subtraction before adding size so end cannot wrap around on
	// platforms where int is narrower than the caller's input.
	if start > b.limit || size > b.limit-start {
		return nil, ErrLimitExceeded
	}
	end := start + size
	if cap(b.data) < end {
		// Grow geometrically without exceeding the budget. Check before
		// doubling so the capacity calculation cannot overflow.
		capacity := b.limit
		if cap(b.data) <= b.limit/2 {
			capacity = max(end, 2*cap(b.data))
		}
		next := make([]byte, start, capacity)
		copy(next, b.data)
		b.data = next
	}
	b.data = b.data[:end]
	return b.data[start:end], nil
}

func (b *LimitBuffer) Write(data []byte) (int, error) {
	destination, err := b.reserve(len(data))
	if err != nil {
		return 0, err
	}
	copy(destination, data)
	return len(data), nil
}

func (b *LimitBuffer) WriteString(data string) (int, error) {
	destination, err := b.reserve(len(data))
	if err != nil {
		return 0, err
	}
	copy(destination, data)
	return len(data), nil
}

func (b *LimitBuffer) Bytes() []byte { return b.data }
func (b *LimitBuffer) Len() int      { return len(b.data) }
func (b *LimitBuffer) Reset()        { b.data = b.data[:0] }

// ReadAllLimit reads at most limit bytes and reports overflow distinctly.
func ReadAllLimit(reader io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		limit = 0
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrLimitExceeded
	}
	return data, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type limitedHTTPClient struct {
	HTTPDoer
	limit int64
}

// LimitBody caps bytes delivered by a response body without buffering it.
// One overflow byte is probed and discarded so callers receive
// ErrLimitExceeded while retained memory stays within the configured limit.
func LimitBody(body io.ReadCloser, limit int64) io.ReadCloser {
	if body == nil {
		return nil
	}
	if limit < 0 {
		limit = 0
	}
	return &limitedBody{ReadCloser: body, remaining: limit}
}

// LimitResponseBody rejects known oversized responses immediately and caps
// unknown-length bodies at their source.
func LimitResponseBody(response *http.Response, limit int64) error {
	if response == nil || response.Body == nil || limit <= 0 {
		return nil
	}
	if response.ContentLength > limit {
		_ = response.Body.Close()
		return ErrLimitExceeded
	}
	response.Body = LimitBody(response.Body, limit)
	return nil
}

// LimitHTTPClient applies LimitResponseBody before an SDK or adapter can
// buffer the upstream response.
func LimitHTTPClient(client HTTPDoer, limit int64) HTTPDoer {
	if client == nil || limit <= 0 {
		return client
	}
	return &limitedHTTPClient{HTTPDoer: client, limit: limit}
}

func (client *limitedHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := client.HTTPDoer.Do(request)
	if err != nil {
		return nil, err
	}
	if err := LimitResponseBody(response, client.limit); err != nil {
		return nil, err
	}
	return response, nil
}

func (body *limitedBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if body.remaining > 0 {
		if int64(len(buffer)) > body.remaining {
			buffer = buffer[:body.remaining]
		}
		read, err := body.ReadCloser.Read(buffer)
		body.remaining -= int64(read)
		return read, err
	}
	var probe [1]byte
	read, err := body.ReadCloser.Read(probe[:])
	if read > 0 {
		return 0, ErrLimitExceeded
	}
	return 0, err
}

func ResponseBodyLimit() int64 {
	megabytes := constant.MaxResponseBodyMB
	if megabytes <= 0 {
		megabytes = 32
	}
	return int64(megabytes) << 20
}

// WebSocketMessageLimit returns the maximum size of a single WebSocket
// message accepted by relay paths. WebSocket connections do not pass through
// the HTTP request-body limiter, so they need an explicit frame ceiling to
// prevent Gorilla from allocating an attacker-controlled message size.
// Keeping this aligned with the bounded upstream response limit preserves the
// existing payload budget while making the previously unbounded path finite.
func WebSocketMessageLimit() int64 {
	return ResponseBodyLimit()
}

// SetWebSocketReadLimit applies the shared message ceiling to a connection.
// A nil connection is accepted so callers can use it safely on optional
// upstream connections during error cleanup.
func SetWebSocketReadLimit(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	conn.SetReadLimit(WebSocketMessageLimit())
}

func ReadResponseBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, io.ErrUnexpectedEOF
	}
	return ReadAllLimit(response.Body, ResponseBodyLimit())
}

// SHA256RequestBody hashes a replayable request body without copying it when
// GetBody is available. The fallback retains at most limit bytes so signing a
// request can never turn an unbounded body into an unbounded duplicate.
func SHA256RequestBody(request *http.Request, limit int64) (string, error) {
	if request == nil {
		return "", io.ErrUnexpectedEOF
	}
	if limit < 0 {
		limit = 0
	}
	if request.Body == nil {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:]), nil
	}
	if request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			return "", err
		}
		defer body.Close()
		return hashReader(body, limit)
	}

	data, err := ReadAllLimit(request.Body, limit)
	if err != nil {
		return "", err
	}
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(data))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return hashReader(bytes.NewReader(data), limit)
}

func hashReader(reader io.Reader, limit int64) (string, error) {
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(reader, limit+1))
	if err != nil {
		return "", err
	}
	if written > limit {
		return "", ErrLimitExceeded
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// MarshalLimit JSON-encodes without ever retaining more than limit bytes.
func MarshalLimit(value any, limit int) ([]byte, error) {
	buffer := NewLimitBuffer(limit)
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	return data, nil
}
