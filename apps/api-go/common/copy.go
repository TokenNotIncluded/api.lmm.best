package common

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/jinzhu/copier"
)

// copier's generic slice path reflects/allocates for every byte. Request DTOs
// contain large RawMessages, so clone these fields in bulk while preserving
// ownership, nil/empty semantics, and the existing deep-copy rules elsewhere.
var requestByteConverters = []copier.TypeConverter{
	{
		SrcType: json.RawMessage(nil), DstType: json.RawMessage(nil),
		Fn: func(src any) (any, error) {
			return json.RawMessage(bytes.Clone(src.(json.RawMessage))), nil
		},
	},
	{
		SrcType: []byte(nil), DstType: []byte(nil),
		Fn: func(src any) (any, error) {
			return bytes.Clone(src.([]byte)), nil
		},
	},
}

func DeepCopy[T any](src *T) (*T, error) {
	if src == nil {
		return nil, fmt.Errorf("copy source cannot be nil")
	}
	var dst T
	err := copier.CopyWithOption(&dst, src, copier.Option{
		DeepCopy: true, IgnoreEmpty: true, Converters: requestByteConverters,
	})
	if err != nil {
		return nil, err
	}
	return &dst, nil
}
