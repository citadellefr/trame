package ot

import (
	"strconv"
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

// BenchmarkDocKeystroke measures a character typed then deleted in the
// middle of a paragraph of each length.
func BenchmarkDocKeystroke(b *testing.B) {
	for _, width := range []int{80, 20000, 1000000} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			d, err := NewDoc(Delta{}.Push(Op{Insert: strings.Repeat("x", width) + "\n"}))
			if err != nil {
				b.Fatal(err)
			}
			typed := Delta{{Retain: width / 2}, {Insert: "a"}}
			deleted := Delta{{Retain: width / 2}, {Delete: 1}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if d.Apply(typed) != nil || d.Apply(deleted) != nil {
					b.Fatal("edit refused")
				}
			}
		})
	}
}
