package humangrant

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
)

//go:embed web/client.mjs
var clientJS []byte

//go:embed web/ui.mjs
var uiJS []byte

//go:embed web/style.css
var styleCSS []byte

var pageTemplate = template.Must(template.New("humangrant").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>HumanGrant</title><link rel="stylesheet" href="/humangrant/style.css"><script type="module" src="/humangrant/ui.js"></script></head>
<body><main><header><a href="/">LMM</a><a href="/human/grants">My AI grants</a></header><h1>HumanGrant</h1><p id="notice" role="status" aria-live="polite"></p><div id="app"></div><pre hidden id="hg-data">{{.JSON}}</pre></main></body></html>`))

func (t *transaction) page(mode string, data any, cookie *http.Cookie) response {
	raw, err := json.Marshal(map[string]any{"mode": mode, "data": data})
	if err != nil {
		t.err = err
		return denied(500, "internal_error")
	}
	var out bytes.Buffer
	// The JSON is HTML-escaped as plain text, then read with textContent. Neither
	// requester text nor JSON is promoted to template.HTML or executable script.
	if err := pageTemplate.Execute(&out, struct{ JSON string }{string(raw)}); err != nil {
		t.err = err
		return denied(500, "internal_error")
	}
	result := response{status: 200, contentType: "text/html; charset=utf-8", body: out.Bytes()}
	if cookie != nil {
		result.cookies = []*http.Cookie{cookie}
	}
	return result
}
