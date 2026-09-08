package common

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jinzhu/copier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type namedCopyBytes []byte

type byteCopyNested struct {
	Raw  json.RawMessage
	Data []byte
}

type byteCopyFixture struct {
	Raw       json.RawMessage
	Data      []byte
	Nested    []byteCopyNested
	Mapping   map[string]json.RawMessage
	Dynamic   any
	Pointer   *json.RawMessage
	Named     namedCopyBytes
	NilRaw    json.RawMessage
	EmptyRaw  json.RawMessage
	NilData   []byte
	EmptyData []byte
	Model     string
}

func TestDeepCopyByteFieldsMatchLegacyValuesAndStayIndependent(t *testing.T) {
	pointed := json.RawMessage(`"pointed"`)
	original := &byteCopyFixture{
		Raw: json.RawMessage(`{"input":"payload"}`), Data: []byte("binary"),
		Nested:  []byteCopyNested{{Raw: json.RawMessage(`"nested"`), Data: []byte("nested bytes")}},
		Mapping: map[string]json.RawMessage{"raw": json.RawMessage(`"mapped"`)},
		Dynamic: json.RawMessage(`"dynamic"`), Pointer: &pointed,
		Named: namedCopyBytes("named"), EmptyRaw: make(json.RawMessage, 0),
		EmptyData: make([]byte, 0), Model: "original",
	}
	var legacy byteCopyFixture
	require.NoError(t, copier.CopyWithOption(&legacy, original, copier.Option{DeepCopy: true, IgnoreEmpty: true}))
	copy, err := DeepCopy(original)
	require.NoError(t, err)
	assert.Equal(t, &legacy, copy, "bulk byte copies must preserve legacy value semantics")
	assert.Nil(t, copy.NilRaw)
	assert.Nil(t, copy.NilData)
	assert.NotNil(t, copy.EmptyRaw)
	assert.NotNil(t, copy.EmptyData)

	copy.Raw[2] = 'X'
	copy.Data[0] = 'X'
	copy.Nested[0].Raw[1] = 'X'
	copy.Nested[0].Data[0] = 'X'
	copy.Mapping["raw"][1] = 'X'
	copy.Dynamic.(json.RawMessage)[1] = 'X'
	(*copy.Pointer)[1] = 'X'
	copy.Named[0] = 'X'
	copy.Model = "mapped"
	assert.JSONEq(t, `{"input":"payload"}`, string(original.Raw))
	assert.Equal(t, "binary", string(original.Data))
	assert.JSONEq(t, `"nested"`, string(original.Nested[0].Raw))
	assert.Equal(t, "nested bytes", string(original.Nested[0].Data))
	assert.JSONEq(t, `"mapped"`, string(original.Mapping["raw"]))
	assert.JSONEq(t, `"dynamic"`, string(original.Dynamic.(json.RawMessage)))
	assert.JSONEq(t, `"pointed"`, string(*original.Pointer))
	assert.Equal(t, "named", string(original.Named))
	assert.Equal(t, "original", original.Model)
}

func TestDeepCopyByteFieldsPreserveRawBytesWithoutJSONParsing(t *testing.T) {
	original := &byteCopyNested{Raw: json.RawMessage{0xff, 0, 'x'}, Data: []byte{0, 0xff, 1}}
	copy, err := DeepCopy(original)
	require.NoError(t, err)
	assert.Equal(t, original, copy)
	copy.Raw[0] = 0
	copy.Data[0] = 1
	assert.Equal(t, byte(0xff), original.Raw[0])
	assert.Equal(t, byte(0), original.Data[0])
}

var byteCopyAllocationSink *byteCopyNested

func TestDeepCopyByteFieldAllocationsDoNotScalePerByte(t *testing.T) {
	allocations := func(size int) float64 {
		original := &byteCopyNested{Raw: bytes.Repeat([]byte("x"), size), Data: bytes.Repeat([]byte("y"), size)}
		return testing.AllocsPerRun(3, func() {
			copy, err := DeepCopy(original)
			if err != nil {
				panic(err)
			}
			byteCopyAllocationSink = copy
		})
	}
	small := allocations(32 << 10)
	large := allocations(1 << 20)
	assert.LessOrEqual(t, large, small+8, "byte fields must be cloned in bulk, not one reflected allocation per byte")
	assert.Less(t, large, float64(100))
}
