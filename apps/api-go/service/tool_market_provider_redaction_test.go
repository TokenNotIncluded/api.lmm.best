package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolMarketProviderRedactionIncludesProtocolErrors(t *testing.T) {
	for _, raw := range []string{
		`{"isError":true,"structuredContent":{"caller":"private-merchant","workspaceId":"private-workspace"},"content":[{"type":"text","text":"private-merchant"}]}`,
		`{"content":[{"type":"text","text":"invalid response for private-merchant"}]}`,
		`{"structuredContent":{"caller":"private-merchant","workspaceId":"private-workspace","input":{"echo":"private-input"},"status":"COMPLETED","output":{"answer":"business-output"},"providerResponse":{"httpStatus":200,"headers":{"x-workspace":"private-workspace"}}},"content":[{"type":"text","text":"private-merchant"}],"_meta":{"caller":"private-merchant"}}`,
	} {
		var input map[string]any
		if err := marketDecodeExactJSON([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		public := marketMonidPublicResult(input)
		encoded, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "private-") {
			t.Fatalf("unfiltered merchant data: %s", encoded)
		}
		if strings.Contains(raw, "business-output") {
			if !strings.Contains(string(encoded), "business-output") {
				t.Fatal("business result was discarded")
			}
		} else if public["isError"] != true {
			t.Fatal("invalid or failed response lost its error marker")
		}
	}
}
