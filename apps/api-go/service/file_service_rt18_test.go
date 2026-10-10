package service_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/ollama"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// RT18 uses small synthetic files and a loopback-only upstream. The account
// labels below are not real authentication and do not verify billing.
type rt18Image struct {
	encoded string
	raw     []byte
	width   int
	height  int
}

func rt18Images(t *testing.T) [2]rt18Image {
	t.Helper()
	var out [2]rt18Image
	var original [2][]byte
	for i, bounds := range []image.Rectangle{image.Rect(0, 0, 32, 16), image.Rect(0, 0, 16, 32)} {
		img := image.NewRGBA(bounds)
		palette := []color.RGBA{{220, 20, 30, 255}, {20, 50, 220, 255}}
		for y := 0; y < bounds.Dy(); y++ {
			for x := 0; x < bounds.Dx(); x++ {
				img.SetRGBA(x, y, palette[i])
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		original[i] = buf.Bytes()
		out[i] = rt18Image{width: bounds.Dx(), height: bounds.Dy()}
	}
	marker := func(data []byte) []byte {
		out := make([]byte, 4+len(data))
		out[0], out[1] = 0xff, 0xfe
		binary.BigEndian.PutUint16(out[2:4], uint16(2+len(data)))
		copy(out[4:], data)
		return out
	}
	common := marker(bytes.Repeat([]byte("rt18"), 40))
	maxLen := max(len(original[0]), len(original[1]))
	for i := range out {
		raw := append([]byte{}, original[i][:2]...)
		raw = append(raw, common...)
		raw = append(raw, marker(bytes.Repeat([]byte{'x'}, maxLen-len(original[i])))...)
		raw = append(raw, original[i][2:]...)
		out[i].raw = raw
		out[i].encoded = base64.StdEncoding.EncodeToString(raw)
		decoded, format, err := image.Decode(bytes.NewReader(raw))
		if err != nil || format != "jpeg" {
			t.Fatalf("sample %d not a valid JPEG: %v, %q", i, err, format)
		}
		if decoded.Bounds().Dx() != out[i].width || decoded.Bounds().Dy() != out[i].height {
			t.Fatal("sample dimensions changed")
		}
	}
	a, b := out[0], out[1]
	if len(a.encoded) <= 128 || len(a.encoded) != len(b.encoded) ||
		a.encoded[:128] != b.encoded[:128] || bytes.Equal(a.raw, b.raw) {
		t.Fatal("sample pair does not meet RT18 preconditions")
	}
	return out
}

func rt18ContextFor(account int) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/rt18", nil)
	c.Set("id", account)
	return c
}

func rt18AssertImage(t *testing.T, data *types.CachedFileData, want rt18Image) {
	t.Helper()
	if data == nil {
		t.Fatal("nil cached file")
	}
	encoded, err := data.GetBase64Data()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.Equal(raw, want.raw) {
		t.Fatalf("cached bytes differ from input: %v", err)
	}
	if data.Size != int64(len(want.raw)) || data.MimeType != "image/jpeg" ||
		data.ImageConfig == nil || data.ImageConfig.Width != want.width ||
		data.ImageConfig.Height != want.height || data.ImageFormat != "jpeg" {
		t.Fatalf("cached size/MIME/image config differ: %+v", data)
	}
}

func TestRT18SameRequestOrderAndReuse(t *testing.T) {
	files := rt18Images(t)
	for _, order := range []struct {
		name string
		idx  [2]int
	}{{"AB", [2]int{0, 1}}, {"BA", [2]int{1, 0}}, {"AA", [2]int{0, 0}}} {
		t.Run(order.name, func(t *testing.T) {
			c := rt18ContextFor(101)
			var objects [2]*types.CachedFileData
			for i, index := range order.idx {
				f := files[index]
				src := types.NewBase64FileSource("data:image/jpeg;base64,"+f.encoded, "image/jpeg")
				got, err := service.LoadFileSource(c, src, "rt18")
				if err != nil {
					t.Fatal(err)
				}
				rt18AssertImage(t, got, f)
				second, err := service.LoadFileSource(c, src, "rt18 cached")
				if err != nil || got != second {
					t.Fatalf("same FileSource missed cache: %v", err)
				}
				objects[i] = got
			}
			if (objects[0] == objects[1]) != (order.idx[0] == order.idx[1]) {
				t.Fatal("different files aliased, or identical files failed to share")
			}
			service.CleanupFileSources(c)
			service.CleanupFileSources(c)
		})
	}
}

func TestRT18InvalidInputAndIndependentContexts(t *testing.T) {
	files := rt18Images(t)
	a := rt18ContextFor(101)
	b := rt18ContextFor(202)
	first, err := service.LoadFileSource(a, types.NewBase64FileSource(files[0].encoded, "image/jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.LoadFileSource(b, types.NewBase64FileSource(files[1].encoded, "image/jpeg"))
	if err != nil || first == second {
		t.Fatalf("independent contexts aliased: %v", err)
	}
	rt18AssertImage(t, first, files[0])
	rt18AssertImage(t, second, files[1])
	invalid := files[1].encoded[:256] + "!" + files[1].encoded[257:]
	if _, err := base64.StdEncoding.DecodeString(invalid); err == nil {
		t.Fatal("invalid fixture unexpectedly decodes")
	}
	badSource := types.NewBase64FileSource(invalid, "image/jpeg")
	if got, err := service.LoadFileSource(a, badSource); err == nil || got != nil || badSource.HasCache() {
		t.Fatalf("invalid input reused cached image: %p %v", got, err)
	}
	cancelled, cancel := context.WithCancel(a.Request.Context())
	a.Request = a.Request.WithContext(cancelled)
	cancel()
	service.CleanupFileSources(a)
	service.CleanupFileSources(a)
	rt18AssertImage(t, second, files[1])
	service.CleanupFileSources(b)
	service.CleanupFileSources(b)
}

func TestRT18SameContentDifferentMIME(t *testing.T) {
	files := rt18Images(t)
	c := rt18ContextFor(101)
	defer service.CleanupFileSources(c)
	a, err := service.LoadFileSource(c, types.NewBase64FileSource(files[0].encoded, "image/jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.LoadFileSource(c, types.NewBase64FileSource(files[0].encoded, "application/octet-stream"))
	if err != nil || a == b || b.MimeType != "application/octet-stream" {
		t.Fatalf("MIME cache identity not preserved: %v", err)
	}
}

func TestRT18LocalOllamaUpstreamBytes(t *testing.T) {
	files := rt18Images(t)
	for _, order := range []struct {
		name string
		idx  [2]int
	}{{"AB", [2]int{0, 1}}, {"BA", [2]int{1, 0}}, {"AA", [2]int{0, 0}}} {
		t.Run(order.name, func(t *testing.T) {
			received := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				body, err := io.ReadAll(io.LimitReader(r.Body, 65537))
				if err != nil || len(body) > 65536 {
					http.Error(w, "body too large", http.StatusBadRequest)
					return
				}
				received <- body
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
					return nil, fmt.Errorf("non-loopback target rejected: %q", address)
				}
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2*time.Second}
			c := rt18ContextFor(101)
			defer service.CleanupFileSources(c)
			parts := make([]any, 0, 2)
			for _, index := range order.idx {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]string{"url": "data:image/jpeg;base64," + files[index].encoded},
				})
			}
			wire, err := json.Marshal(map[string]any{
				"model": "rt18-local-only",
				"messages": []any{map[string]any{"role": "user", "content": parts}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var request dto.GeneralOpenAIRequest
			if err := json.Unmarshal(wire, &request); err != nil {
				t.Fatal(err)
			}
			converted, err := (&ollama.Adaptor{}).ConvertOpenAIRequest(c,
				&relaycommon.RelayInfo{RequestURLPath: "/v1/chat/completions"}, &request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(converted)
			if err != nil {
				t.Fatal(err)
			}
			outbound, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
				upstream.URL+"/api/chat", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(outbound)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("upstream response %d", resp.StatusCode)
			}
			select {
			case captured := <-received:
				var payload struct {
					Messages []struct {
						Images []string `json:"images"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(captured, &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload.Messages) != 1 || len(payload.Messages[0].Images) != 2 {
					t.Fatalf("missing two upstream images: %s", captured)
				}
				for i, encoded := range payload.Messages[0].Images {
					raw, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil || !bytes.Equal(raw, files[order.idx[i]].raw) {
						t.Errorf("upstream image %d does not match input: %v", i, err)
					}
				}
			default:
				t.Fatal("local upstream received no request")
			}
		})
	}
}

func TestRT18ParallelSameContextRegistration(t *testing.T) {
	files := rt18Images(t)
	c := rt18ContextFor(101)
	defer service.CleanupFileSources(c)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			f := files[index%2]
			got, err := service.LoadFileSource(c, types.NewBase64FileSource(f.encoded, "image/jpeg"))
			if err != nil {
				errs <- err
				return
			}
			gotEncoded, err := got.GetBase64Data()
			if err != nil || gotEncoded != f.encoded {
				errs <- fmt.Errorf("concurrent load %d mixed files: %v", index, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	registered, ok := c.Get(string(constant.ContextKeyFileSourcesToCleanup))
	if !ok {
		t.Fatal("cleanup registry missing")
	}
	sources, ok := registered.([]types.FileSource)
	if !ok || len(sources) != workers {
		t.Fatalf("lost concurrent cleanup registrations: got=%T len=%d want=%d", registered, len(sources), workers)
	}
	seen := map[types.FileSource]bool{}
	for _, source := range sources {
		if seen[source] {
			t.Fatal("source added to cleanup list twice")
		}
		seen[source] = true
	}
}

func TestRT18SharedSourceCannotCrossActiveRequests(t *testing.T) {
	files := rt18Images(t)
	ca := rt18ContextFor(101)
	cb := rt18ContextFor(202)
	source := types.NewBase64FileSource(files[0].encoded, "image/jpeg")
	first, err := service.LoadFileSource(ca, source)
	if err != nil {
		t.Fatal(err)
	}
	if unexpected, err := service.LoadFileSource(cb, source); err == nil || unexpected != nil {
		t.Fatalf("active request source was borrowed by a different context: %p %v", unexpected, err)
	}
	rt18AssertImage(t, first, files[0])
	service.CleanupFileSources(ca)
	service.CleanupFileSources(ca)
	second, err := service.LoadFileSource(cb, source)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("closed object escaped from old request")
	}
	rt18AssertImage(t, second, files[0])
	if got, err := service.LoadFileSource(ca, types.NewBase64FileSource(files[1].encoded, "image/jpeg")); err == nil || got != nil {
		t.Fatalf("load succeeded on closed request: %p %v", got, err)
	}
	service.CleanupFileSources(cb)
}

func TestRT18CleanupRacesAgainstBoundedLoads(t *testing.T) {
	files := rt18Images(t)
	for attempt := 0; attempt < 12; attempt++ {
		c := rt18ContextFor(101)
		const workers = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				f := files[index%2]
				_, err := service.LoadFileSource(c, types.NewBase64FileSource(f.encoded, "image/jpeg"))
				if err != nil && !strings.Contains(err.Error(), "after request cleanup") {
					errs <- err
				}
			}(i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			service.CleanupFileSources(c)
		}()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		registered, _ := c.Get(string(constant.ContextKeyFileSourcesToCleanup))
		sources, _ := registered.([]types.FileSource)
		if len(sources) != 0 {
			t.Fatalf("request cleanup left %d registered sources", len(sources))
		}
	}
}
