package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestBuiltinToolMarketRegistryPrecisionPreservesNativeOutputMetadataAndImage(t *testing.T) {
	type preciseValue struct {
		Integer int64       `json:"integer"`
		Decimal json.Number `json:"decimal"`
	}
	imageBytes := []byte("native-image-fixture")
	for _, explicitImage := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit_image_%t", explicitImage), func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "native-precision-test", Version: "1"}, nil)
			schema := &jsonschema.Schema{
				Type: "object", Properties: map[string]*jsonschema.Schema{
					"integer": {Type: "integer"}, "decimal": {Type: "number"},
				}, Required: []string{"integer", "decimal"},
			}
			inputs := make(chan preciseValue, 1)
			addToolMarketBuiltinMCPTool(server, &mcp.Tool{Name: "native.precision", InputSchema: schema, OutputSchema: schema}, func(ctx context.Context, request *mcp.CallToolRequest, input preciseValue) (*mcp.CallToolResult, preciseValue, error) {
				inputs <- input
				result := &mcp.CallToolResult{Meta: mcp.Meta{"sequence": input.Integer, "nested": map[string]any{"fraction": json.Number("0.12345678901234567890123456789")}}}
				if explicitImage {
					result.Content = []mcp.Content{&mcp.ImageContent{
						MIMEType: "image/png", Data: imageBytes,
						Meta: mcp.Meta{"sequence": int64(9007199254740995), "nested": map[string]any{"fraction": json.Number("0.12345678901234567890123456789")}},
					}}
				}
				return result, input, nil
			})
			result, err := callBuiltinToolMarketServer(context.Background(), server, &auth.TokenInfo{UserID: "17"}, &mcp.CallToolParams{Name: "native.precision", Arguments: json.RawMessage(`{"integer":9007199254740993,"decimal":1.234567890123456789}`)})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.False(t, result.NeedsInput())
			require.IsType(t, json.RawMessage{}, result.StructuredContent)
			typedInput := <-inputs
			require.EqualValues(t, 9007199254740993, typedInput.Integer)
			require.Equal(t, json.Number("1.234567890123456789"), typedInput.Decimal)
			require.Equal(t, json.Number("9007199254740993"), result.Meta["sequence"])
			require.Equal(t, json.Number("0.12345678901234567890123456789"), result.Meta["nested"].(map[string]any)["fraction"])
			require.Len(t, result.Content, 1)
			if explicitImage {
				image, ok := result.Content[0].(*mcp.ImageContent)
				require.True(t, ok)
				require.Equal(t, "image/png", image.MIMEType)
				require.Equal(t, imageBytes, image.Data)
				require.Equal(t, json.Number("9007199254740995"), image.Meta["sequence"])
				require.Equal(t, json.Number("0.12345678901234567890123456789"), image.Meta["nested"].(map[string]any)["fraction"])
			} else {
				text, ok := result.Content[0].(*mcp.TextContent)
				require.True(t, ok)
				require.Equal(t, string(result.StructuredContent.(json.RawMessage)), text.Text)
			}
			data, err := json.Marshal(result)
			require.NoError(t, err)
			var wire map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &wire))
			var structured, metadata, nested map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(wire["structuredContent"], &structured))
			require.Equal(t, "9007199254740993", string(structured["integer"]))
			require.Equal(t, "1.234567890123456789", string(structured["decimal"]))
			require.NoError(t, json.Unmarshal(wire["_meta"], &metadata))
			require.Equal(t, "9007199254740993", string(metadata["sequence"]))
			require.NoError(t, json.Unmarshal(metadata["nested"], &nested))
			require.Equal(t, "0.12345678901234567890123456789", string(nested["fraction"]))
			if explicitImage {
				var contents []map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(wire["content"], &contents))
				require.NoError(t, json.Unmarshal(contents[0]["_meta"], &metadata))
				require.Equal(t, "9007199254740995", string(metadata["sequence"]))
				require.NoError(t, json.Unmarshal(metadata["nested"], &nested))
				require.Equal(t, "0.12345678901234567890123456789", string(nested["fraction"]))
			}
		})
	}
}

func TestBuiltinToolMarketRegistryPrecisionPreservesSDKDefaults(t *testing.T) {
	type preciseInput struct {
		Integer int64 `json:"integer"`
		Count   int64 `json:"count"`
	}
	type preciseOutput struct {
		Integer int64 `json:"integer"`
		Nested  struct {
			Integer int64 `json:"integer"`
		} `json:"nested"`
		Items []json.Number `json:"items"`
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "defaults-precision-test", Version: "1"}, nil)
	inputs := make(chan preciseInput, 1)
	addToolMarketBuiltinMCPTool(server, &mcp.Tool{
		Name: "native.defaults",
		InputSchema: &jsonschema.Schema{Type: "object", Required: []string{"integer"}, Properties: map[string]*jsonschema.Schema{
			"integer": {Type: "integer"}, "count": {Type: "integer", Default: json.RawMessage(`7`)},
		}},
		OutputSchema: &jsonschema.Schema{Type: "object", Required: []string{"integer"}, Properties: map[string]*jsonschema.Schema{
			"integer":  {Type: "integer"},
			"currency": {Type: "string", Default: json.RawMessage(`"CNY"`)},
			"nested": {Type: "object", Properties: map[string]*jsonschema.Schema{
				"integer": {Type: "integer"}, "enabled": {Type: "boolean", Default: json.RawMessage(`true`)},
			}},
			"items": {Type: "array", Items: &jsonschema.Schema{Type: "number"}},
		}},
	}, func(ctx context.Context, request *mcp.CallToolRequest, input preciseInput) (*mcp.CallToolResult, preciseOutput, error) {
		inputs <- input
		output := preciseOutput{Integer: input.Integer, Items: []json.Number{json.Number("-9007199254740995"), json.Number("1.234567890123456789")}}
		output.Nested.Integer = input.Integer
		return nil, output, nil
	})
	result, err := callBuiltinToolMarketServer(context.Background(), server, &auth.TokenInfo{UserID: "17"}, &mcp.CallToolParams{Name: "native.defaults", Arguments: json.RawMessage(`{"integer":9007199254740993}`)})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, preciseInput{Integer: 9007199254740993, Count: 7}, <-inputs)
	var output, nested map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(result.StructuredContent.(json.RawMessage), &output))
	require.Equal(t, "9007199254740993", string(output["integer"]))
	require.Equal(t, `"CNY"`, string(output["currency"]))
	require.NoError(t, json.Unmarshal(output["nested"], &nested))
	require.Equal(t, "9007199254740993", string(nested["integer"]))
	require.Equal(t, "true", string(nested["enabled"]))
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(output["items"], &items))
	require.Equal(t, "-9007199254740995", string(items[0]))
	require.Equal(t, "1.234567890123456789", string(items[1]))
	require.Len(t, result.Content, 1)
	require.Equal(t, string(result.StructuredContent.(json.RawMessage)), result.Content[0].(*mcp.TextContent).Text)
}

func TestBuiltinToolMarketRegistryPrecisionPreservesInputRequiredResult(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "confirmation-precision-test", Version: "1"}, nil)
	addToolMarketBuiltinMCPTool(server, &mcp.Tool{Name: "native.confirmation"}, func(ctx context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Meta: mcp.Meta{"sequence": int64(9007199254740995)}, RequestState: "original-confirmation-state",
			InputRequests: mcp.InputRequestMap{"confirmation": &mcp.ElicitParams{
				Mode: "form", Message: "Confirm the original precise action.",
				RequestedSchema: &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{"confirmed": {Type: "boolean"}}, Required: []string{"confirmed"}},
			}},
		}, struct{}{}, nil
	})
	result, err := callBuiltinToolMarketServer(context.Background(), server, &auth.TokenInfo{UserID: "17"}, &mcp.CallToolParams{Name: "native.confirmation"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.True(t, result.NeedsInput())
	require.Nil(t, result.StructuredContent)
	require.Equal(t, "original-confirmation-state", result.RequestState)
	require.Equal(t, json.Number("9007199254740995"), result.Meta["sequence"])
	confirmation, ok := result.InputRequests["confirmation"].(*mcp.ElicitParams)
	require.True(t, ok)
	require.Equal(t, "form", confirmation.Mode)
	require.Equal(t, "Confirm the original precise action.", confirmation.Message)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &wire))
	require.Equal(t, `"input_required"`, string(wire["resultType"]))
	require.Contains(t, string(wire["inputRequests"]), `"confirmation"`)
	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire["_meta"], &metadata))
	require.Equal(t, "9007199254740995", string(metadata["sequence"]))
}
