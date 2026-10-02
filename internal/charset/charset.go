// Package charset reads and writes Windows-1252, the encoding of the text
// files Windows writes that are not UTF-8.
package charset

import (
	"strings"
	"unicode/utf8"
)

// high are the characters of 0x80–0x9F, where Windows-1252 differs from
// Latin-1; those it leaves undefined map to themselves.
var high = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8D, 'Ž', 0x8F,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9D, 'ž', 'Ÿ',
}

// Decode reads Windows-1252.
func Decode(data []byte) string {
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		if 0x80 <= c && c < 0xA0 {
			b.WriteRune(high[c-0x80])
		} else {
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

// Encode writes s in Windows-1252, and tells whether it holds only
// characters the encoding has.
func Encode(s string) ([]byte, bool) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80 || 0xA0 <= r && r <= 0xFF:
			out = append(out, byte(r))
		case r == utf8.RuneError:
			return nil, false
		default:
			i := 0
			for i < len(high) && high[i] != r {
				i++
			}
			if i == len(high) {
				return nil, false
			}
			out = append(out, byte(0x80+i))
		}
	}
	return out, true
}
