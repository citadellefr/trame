package ot

import (
	"strings"
	"testing"
)

// BenchmarkDocDelta measures what a client connecting to a document of 200
// pages costs: its whole flow as one delta.
func BenchmarkDocDelta(b *testing.B) {
	d, err := NewDoc(Delta{}.Push(Op{Insert: strings.Repeat(strings.Repeat("lorem ipsum ", 40)+"\n", 2000)}))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for range b.N {
		d.Delta()
	}
}
