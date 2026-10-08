package common

import (
	"bytes"
	"testing"
)

func TestLimitBufferChunkedWriteAllocations(t *testing.T) {
	chunk := bytes.Repeat([]byte{'x'}, 1024)
	want := bytes.Repeat(chunk, 64)
	allocations := testing.AllocsPerRun(3, func() {
		buffer := NewLimitBuffer(len(want))
		for i := 0; i < 64; i++ {
			if _, err := buffer.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(buffer.Bytes(), want) || cap(buffer.data) > len(want) {
			t.Fatal("chunked writes changed the content or exceeded the budget")
		}
	})
	// Allow compiler and implementation overhead, but not one allocation per chunk.
	if allocations > 16 {
		t.Fatalf("64 chunks required %.0f allocations, want at most 16", allocations)
	}
}

func BenchmarkLimitBufferChunkedWrite64K(b *testing.B) {
	chunk := bytes.Repeat([]byte{'x'}, 1024)
	b.ReportAllocs()
	b.SetBytes(64 << 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer := NewLimitBuffer(64 << 10)
		for j := 0; j < 64; j++ {
			if _, err := buffer.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
	}
}
