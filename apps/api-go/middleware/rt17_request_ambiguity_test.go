package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
)

// These assertions call the real ingress parser and the real reusable DTO
// decoder. They do NOT replace the full relay/upstream/billing acceptance test.
// A rejection is allowed for ambiguous input. If accepted, the stages must
// agree. Do not reverse this invariant to preserve a first-key/last-key split.
func TestRT17ModelParserContinuity(t *testing.T) {
	tests := []struct {
		name, fields string
		mustAccept   bool
	}{
		{"ordinary", `"model":"rt17-cheap"`, true},
		{"unique-escaped-name", `"mo\u0064el":"rt17-cheap"`, true},
		{"nested-name-is-not-top-level", `"model":"rt17-cheap","metadata":{"model":"rt17-other"}`, true},
		{"duplicate-distinct", `"model":"rt17-cheap","model":"rt17-expensive"`, false},
		{"duplicate-reversed", `"model":"rt17-expensive","model":"rt17-cheap"`, false},
		{"case-alias", `"model":"rt17-cheap","Model":"rt17-expensive"`, false},
		{"escaped-alias", `"model":"rt17-cheap","mo\u0064el":"rt17-expensive"`, false},
		{"identical-duplicate", `"model":"rt17-cheap","model":"rt17-cheap"`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{` + tc.fields + `,"messages":[{"role":"user","content":"RT17_BODY_A"}]}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json; charset=utf-8")
			defer common.CleanupBodyStorage(c)
			ingress, err := getModelFromRequest(c)
			if err != nil {
				if tc.mustAccept {
					t.Fatalf("valid ingress rejected: %v", err)
				}
				t.Logf("ingress rejected ambiguous input: %v", err)
				return
			}
			var decoded dto.GeneralOpenAIRequest
			if err := common.UnmarshalBodyReusable(c, &decoded); err != nil {
				if tc.mustAccept {
					t.Fatalf("valid DTO rejected: %v", err)
				}
				t.Logf("DTO rejected ambiguous input: %v", err)
				return
			}
			t.Logf("raw=%s ingress_model=%q dto_model=%q", raw, ingress.Model, decoded.Model)
			if ingress.Model != decoded.Model {
				t.Errorf("RT17 parser disagreement: ingress=%q DTO=%q", ingress.Model, decoded.Model)
			}
			if tc.mustAccept {
				if ingress.Model != "rt17-cheap" {
					t.Errorf("valid model changed: %q", ingress.Model)
				}
				storage, err := common.GetBodyStorage(c)
				if err != nil {
					t.Fatal(err)
				}
				remaining, err := storage.Bytes()
				if err != nil {
					t.Fatal(err)
				}
				if string(remaining) != raw {
					t.Errorf("valid reusable body changed: %q", remaining)
				}
			}
		})
	}
}

func TestRT17PlaygroundGroupParserContinuity(t *testing.T) {
	tests := []struct {
		name, fields string
		mustAccept   bool
	}{
		{"ordinary", `"group":"rt17-a"`, true},
		{"duplicate", `"group":"rt17-a","group":"rt17-b"`, false},
		{"case-alias", `"group":"rt17-a","Group":"rt17-b"`, false},
		{"escaped-alias", `"group":"rt17-a","gro\u0075p":"rt17-b"`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"model":"rt17-cheap",` + tc.fields + `,"messages":[{"role":"user","content":"RT17_GROUP"}]}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")
			defer common.CleanupBodyStorage(c)
			ingress, err := getModelFromRequest(c)
			if err != nil {
				if tc.mustAccept {
					t.Fatalf("valid ingress rejected: %v", err)
				}
				t.Logf("ingress rejected: %v", err)
				return
			}
			var decoded dto.PlayGroundRequest
			if err := common.UnmarshalBodyReusable(c, &decoded); err != nil {
				if tc.mustAccept {
					t.Fatalf("valid DTO rejected: %v", err)
				}
				t.Logf("DTO rejected: %v", err)
				return
			}
			t.Logf("raw=%s ingress_group=%q playground_group=%q", raw, ingress.Group, decoded.Group)
			if ingress.Group != decoded.Group {
				t.Errorf("RT17 group disagreement: ingress=%q DTO=%q", ingress.Group, decoded.Group)
			}
			if tc.mustAccept && ingress.Group != "rt17-a" {
				t.Errorf("valid group changed: %q", ingress.Group)
			}
		})
	}
}
