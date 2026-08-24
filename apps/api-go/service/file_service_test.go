package service

import (
	"encoding/binary"
	"testing"
)

func TestParseHEIFDimensionsRejectsExcessiveNesting(t *testing.T) {
	shallow := nestedHEIF(2)
	width, height, ok := parseHEIFDimensions(shallow)
	if !ok || width != 1 || height != 1 {
		t.Fatalf("expected shallow HEIF to parse as 1x1, got %dx%d, ok=%v", width, height, ok)
	}

	width, height, ok = parseHEIFDimensions(nestedHEIF(maxHEIFBoxDepth))
	if !ok || width != 1 || height != 1 {
		t.Fatalf("expected maximum-depth HEIF to parse as 1x1, got %dx%d, ok=%v", width, height, ok)
	}

	if _, _, ok := parseHEIFDimensions(nestedHEIF(maxHEIFBoxDepth + 1)); ok {
		t.Fatal("expected excessively nested HEIF metadata to be rejected")
	}
}

func nestedHEIF(depth int) []byte {
	box := make([]byte, 20)
	binary.BigEndian.PutUint32(box[0:4], 20)
	copy(box[4:8], "ispe")
	binary.BigEndian.PutUint32(box[12:16], 1)
	binary.BigEndian.PutUint32(box[16:20], 1)

	for i := 0; i < depth; i++ {
		container := make([]byte, 8+len(box))
		binary.BigEndian.PutUint32(container[0:4], uint32(len(container)))
		copy(container[4:8], "ipco")
		copy(container[8:], box)
		box = container
	}

	meta := make([]byte, 12+len(box))
	binary.BigEndian.PutUint32(meta[0:4], uint32(len(meta)))
	copy(meta[4:8], "meta")
	copy(meta[12:], box)

	ftyp := make([]byte, 16)
	binary.BigEndian.PutUint32(ftyp[0:4], uint32(len(ftyp)))
	copy(ftyp[4:8], "ftyp")
	copy(ftyp[8:12], "heic")

	return append(ftyp, meta...)
}
