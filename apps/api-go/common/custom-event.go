// Copyright 2014 Manu Martinez-Almeida.  All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package common

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type stringWrapper struct {
	io.Writer
}

func (w stringWrapper) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err == nil && n < len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}

// Server-Sent Events
// W3C Working Draft 29 October 2009
// http://www.w3.org/TR/2009/WD-eventsource-20091029/

var dataReplacer = strings.NewReplacer("\r", "\\r")

// CustomEvent does not synchronize writes to the response writer. Streaming
// callers must serialize event writes at the stream level.
type CustomEvent struct {
	Event string
	Id    string
	Retry uint
	Data  interface{}
}

func (r CustomEvent) Render(w http.ResponseWriter) error {
	data, ok := r.Data.(string)
	if !ok {
		return fmt.Errorf("custom event data must be a string, got %T", r.Data)
	}
	r.WriteContentType(w)
	// Keep all writes on the checked path, including the event delimiter.
	writer := stringWrapper{w}
	if _, err := dataReplacer.WriteString(writer, data); err != nil {
		return err
	}
	if strings.HasPrefix(data, "data") {
		_, err := writer.Write([]byte("\n\n"))
		return err
	}
	return nil
}

func (r CustomEvent) WriteContentType(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")

	if _, exist := header["Cache-Control"]; !exist {
		header.Set("Cache-Control", "no-cache")
	}
}
