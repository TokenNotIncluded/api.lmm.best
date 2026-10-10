// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"bytes"
	"strings"
)

// assistantSSEDecoder accepts the relay's data-only events and standard SSE
// fields. Only a blank line dispatches an event; EOF is never a delimiter.
// The normalized relay stream ends with one fully framed data: [DONE] event.
type assistantSSEDecoder struct {
	buffer bytes.Buffer
	data   bytes.Buffer
	fields int
	skipLF bool
	done   bool
}

func (d *assistantSSEDecoder) feed(input []byte, dispatch func(string)) {
	if d == nil {
		return
	}
	for _, b := range input {
		if d.done {
			return
		}
		if d.skipLF {
			d.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b != '\r' && b != '\n' {
			d.buffer.WriteByte(b)
			continue
		}
		// CR completes the line now. Skip its optional LF on the next byte,
		// including when the next byte arrives in a different HTTP chunk.
		d.skipLF = b == '\r'
		line := d.buffer.Bytes()
		if len(line) == 0 {
			if d.fields > 0 {
				payload := strings.TrimSuffix(d.data.String(), "\n")
				d.done = d.fields == 1 && strings.TrimSpace(payload) == "[DONE]"
				d.data.Reset()
				d.fields = 0
				dispatch(payload)
			}
		} else if bytes.HasPrefix(line, []byte("data:")) {
			value := line[len("data:"):]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			d.data.Write(value)
			d.data.WriteByte('\n')
			d.fields++
		}
		d.buffer.Reset()
	}
}

// Discard the uncommitted tail. Keep the existing call shape, but never invoke
// the callback at EOF: doing so could promote truncated content or a terminator.
func (d *assistantSSEDecoder) flush(_ func(string)) {
	if d == nil {
		return
	}
	d.buffer = bytes.Buffer{}
	d.data = bytes.Buffer{}
	d.fields = 0
	d.skipLF = false
}
