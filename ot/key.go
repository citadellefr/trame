package ot

import "strings"

// Keys order siblings: strings of base 62 digits, compared byte by byte,
// which never end with the smallest digit so that there is always room
// before them. A key can always be found between two others.
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// KeyBetween is a key after a and before b; an empty a is the start, an
// empty b the end.
func KeyBetween(a, b string) string {
	if b != "" && a >= b {
		panic("ot: KeyBetween(" + a + ", " + b + ")")
	}
	return midpoint(a, b)
}

func midpoint(a, b string) string {
	if b != "" {
		n := 0
		for n < len(b) && digitAt(a, n) == b[n] {
			n++
		}
		if n > 0 {
			return b[:n] + midpoint(tail(a, n), b[n:])
		}
	}
	lo := strings.IndexByte(digits, digitAt(a, 0))
	hi := len(digits)
	if b != "" {
		hi = strings.IndexByte(digits, b[0])
	}
	if hi-lo > 1 {
		return digits[(lo+hi+1)/2 : (lo+hi+1)/2+1]
	}
	if len(b) > 1 {
		return b[:1]
	}
	return digits[lo:lo+1] + midpoint(tail(a, 1), "")
}

func digitAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return digits[0]
}

func tail(s string, n int) string {
	if n < len(s) {
		return s[n:]
	}
	return ""
}

// Keys are n increasing keys of the same length, spread evenly.
func Keys(n int) []string {
	width, span := 1, len(digits)
	for span <= n {
		width++
		span *= len(digits)
	}
	out := make([]string, n)
	buf := make([]byte, width)
	for i := range out {
		v := (i + 1) * span / (n + 1)
		for j := width - 1; j >= 0; j-- {
			buf[j] = digits[v%len(digits)]
			v /= len(digits)
		}
		out[i] = strings.TrimRight(string(buf), digits[:1])
	}
	return out
}
