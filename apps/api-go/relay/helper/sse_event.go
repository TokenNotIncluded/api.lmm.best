package helper

import (
	"bufio"
	"errors"
	"strings"
)

var ErrSSEEventTooLarge = errors.New("upstream SSE event exceeds response body limit")

// sseEventDecoder assembles data fields, independently of the payload format.
// EOF does not dispatch an unfinished event. A bare [DONE] line remains an
// explicit legacy terminator; a data: marker is interpreted only at a boundary.
type sseEventDecoder struct {
	data    strings.Builder
	fields  int
	limit   int64
	started bool
}

func (d *sseEventDecoder) dispatch() (string, bool, bool, error) {
	if d.fields == 0 {
		return "", false, false, nil
	}
	payload, fields := d.data.String(), d.fields
	d.data.Reset()
	d.fields = 0
	if fields == 1 && strings.TrimSpace(payload) == "[DONE]" {
		return "", false, true, nil
	}
	return payload, true, false, nil
}

func (d *sseEventDecoder) line(line string) (string, bool, bool, error) {
	if !d.started {
		line = strings.TrimPrefix(line, "\uFEFF")
		d.started = true
	}
	if line == "" {
		return d.dispatch()
	}
	if strings.TrimSpace(line) == "[DONE]" {
		payload, ready, _, err := d.dispatch()
		return payload, ready, true, err
	}
	field, value, _ := strings.Cut(line, ":")
	if field != "data" {
		return "", false, false, nil
	}
	value = strings.TrimPrefix(value, " ") // SSE removes exactly one optional space.
	addition := int64(len(value))
	if d.fields > 0 {
		addition++
	}
	if d.limit > 0 && int64(d.data.Len()) > d.limit-addition {
		return "", false, false, ErrSSEEventTooLarge
	}
	if d.fields > 0 {
		d.data.WriteByte('\n')
	}
	d.data.WriteString(value)
	d.fields++
	return "", false, false, nil
}

// A trailing CR completes the line immediately. Its optional LF is skipped on
// the next invocation, even when CRLF is split between upstream reads.
func splitSSELines() bufio.SplitFunc {
	skipLF := false
	return func(data []byte, atEOF bool) (int, []byte, error) {
		offset := 0
		if skipLF && len(data) > 0 {
			skipLF = false
			if data[0] == '\n' {
				offset = 1
			}
		}
		for i := offset; i < len(data); i++ {
			b := data[i]
			if b == '\r' || b == '\n' {
				skipLF = b == '\r'
				return i + 1, data[offset:i], nil
			}
		}
		if atEOF && len(data) > offset {
			return len(data), data[offset:], nil
		}
		return offset, nil, nil
	}
}
