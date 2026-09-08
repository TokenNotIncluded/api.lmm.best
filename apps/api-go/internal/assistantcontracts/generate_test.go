package assistantcontracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateRequestBindingAndSharedQueryHelpers(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"router/routes.go": `package router
import "github.com/LIghtJUNction/api.lmm.best/controller"
var route = struct{ Handler any }{Handler: controller.UpdateThing}
var other = controller.UnknownThing
`,
		"controller/thing.go": "package controller\nimport (\"github.com/gin-gonic/gin\"; \"external.invalid/types\")\n" +
			"type Embedded struct { Name string `json:\"name\" binding:\"required\"`; Hidden string `json:\"-\"` }\n" +
			"type Request struct { Embedded; Action string `json:\"action\"`; Enabled *bool `json:\"enabled,omitempty\"`; Entries []string `json:\"entries\"`; Weights map[string]float64 `json:\"weights\"` }\n" +
			`func readID(c *gin.Context, name string) { c.Param(name); c.DefaultQuery("limit", "20") }
func UpdateThing(c *gin.Context) { var req Request; c.ShouldBindJSON(&req); readID(c,"thing_id"); switch req.Action {case "enable": case "disable":} }
func UnknownThing(c *gin.Context) { var req types.Unavailable; c.ShouldBindJSON(&req) }
`,
	}
	for path, contents := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	var contracts map[string]map[string]any
	if err := json.Unmarshal(data, &contracts); err != nil {
		t.Fatal(err)
	}
	got := contracts["UpdateThing"]
	if got["contract_status"] != "derived" {
		t.Fatalf("unexpected incomplete contract: %v", got)
	}
	body := got["body_schema"].(map[string]any)
	fields := body["properties"].(map[string]any)
	if _, exists := fields["Hidden"]; exists {
		t.Fatal("json:- field leaked")
	}
	if fields["name"].(map[string]any)["type"] != "string" {
		t.Fatal("embedded field was not flattened")
	}
	if len(fields["action"].(map[string]any)["examples"].([]any)) != 2 {
		t.Fatal("action switch cases were not documented")
	}
	if _, ok := fields["enabled"].(map[string]any)["anyOf"]; !ok {
		t.Fatal("nullable pointer was lost")
	}
	if fields["weights"].(map[string]any)["additionalProperties"].(map[string]any)["type"] != "number" {
		t.Fatal("map value schema was lost")
	}
	if len(body["required"].([]any)) != 1 || body["required"].([]any)[0] != "name" {
		t.Fatal("binding required/omitempty semantics changed")
	}
	if _, ok := got["path_schema"].(map[string]any)["properties"].(map[string]any)["thing_id"]; !ok {
		t.Fatal("helper path argument was lost")
	}
	if got["query_schema"].(map[string]any)["properties"].(map[string]any)["limit"].(map[string]any)["default"] != "20" {
		t.Fatal("query default was lost")
	}
	if contracts["UnknownThing"]["contract_status"] != "partial" {
		t.Fatal("unsupported types must never claim a complete contract")
	}
}
