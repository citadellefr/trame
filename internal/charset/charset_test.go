package charset

import "testing"

func TestRoundTrip(t *testing.T) {
	var all []byte
	for c := range 256 {
		all = append(all, byte(c))
	}
	back, ok := Encode(Decode(all))
	if !ok || string(back) != string(all) {
		t.Fatalf("%v %q", ok, back)
	}
	if got := Decode([]byte{0x80, 'e', 0xE9}); got != "€eé" {
		t.Fatal(got)
	}
	if _, ok := Encode("€ 😀"); ok {
		t.Fatal("an emoji written in Windows-1252")
	}
}
