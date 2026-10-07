package ot

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

var editSeeds = []string{
	`[]`,
	`null`,
	`[{}]`,
	`[{"o":"txt","id":"body","x":[{"r":3},{"i":"a"}]}]`,
	`[{"o":"txt","id":"body","x":[{"i":"line\nbreak \"quoted\" \\ \u00e9 \u0000"},{"d":2},{"r":1,"a":{"b":"1","c":""}}]}]`,
	`[{"o":"new","id":"s2","t":"slide","k":"V","a":{"hidden":true,"n":{"a":[1, 2]}}},{"o":"set","id":"s1","k":"F","a":{"hidden":null}},{"o":"del","id":"s3"}]`,
	`[{"o":"new","id":"t","t":"sheet","k":"V","c":[]},{"o":"cel","id":"t","c":[[1,2,{"v":"x"}],[1,3,{"v":null}]]}]`,
	`[{"o":"ins","id":"t","dim":"r","at":5,"n":2}]`,
	`[{"o":"new","id":"a","t":"shape","k":"V","x":[{"i":"\ud83d\ude00\n"}]}]`,
	`[{"o":"new","id":"a","t":"shape","k":"V","x":[{"i":"😀\n"}]}]`,
	"[{\"o\":\"txt\",\"id\":\"b\",\"x\":[{\"i\":\"\xff\"}]}]",
	` [ { "o" : "txt" , "id" : "body" , "x" : [ { "i" : "a" } ] } ] `,
	`[{"o":"txt","o":"set","id":"a"}]`,
	`[{"O":"txt","ID":"a"}]`,
	`[{"o":"txt","id":"a","extra":1}]`,
	`[{"o":"txt","id":"a","x":null}]`,
	`[{"o":"txt","id":"a","x":[{"r":1.5}]}]`,
	`[{"o":"txt","id":"a","x":[{"r":01}]}]`,
	`[{"o":"txt","id":"a","x":[{"r":-1}]}]`,
	`[{"o":"txt","id":"a","x":[{"r":1e2}]}]`,
	`[{"o":"txt","id":"a","x":[{"r":99999999999999999999}]}]`,
	`[{"o":"set","id":"a","a":{"x":"<b>&"}}]`,
	`[{"o":"txt","id":"a","x":[{"i":"a"}]}`,
	`[{"o":"txt","id":"a","x":[{"i":"a"}]}]]`,
	`{"o":"txt"}`,
}

func plainDecode(data []byte) (Edit, error) {
	var e Edit
	err := json.Unmarshal(data, (*plainEdit)(&e))
	return e, err
}

func checkDecode(t *testing.T, data []byte) {
	t.Helper()
	want, werr := plainDecode(data)
	got, exact, err := DecodeEdit(data)
	if (err == nil) != (werr == nil) {
		t.Fatalf("%q: got error %v, encoding/json %v", data, err, werr)
	}
	if err != nil {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q:\n got %#v\nwant %#v", data, got, want)
	}
	if exact {
		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil || !bytes.Equal(compact.Bytes(), data) {
			t.Fatalf("%q is not exact", data)
		}
	}
}

func TestDecodeEdit(t *testing.T) {
	for _, s := range editSeeds {
		checkDecode(t, []byte(s))
	}
	_, exact, _ := DecodeEdit([]byte(editSeeds[3]))
	_, loose, _ := DecodeEdit([]byte(editSeeds[11]))
	if !exact || loose {
		t.Fatalf("exact = %v, %v", exact, loose)
	}
}

func FuzzDecodeEdit(f *testing.F) {
	for _, s := range editSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		checkDecode(t, data)
	})
}

func BenchmarkDecodeEdit(b *testing.B) {
	data := []byte(`[{"o":"txt","id":"body","x":[{"r":24000},{"i":"a"}]}]`)
	b.ReportAllocs()
	for range b.N {
		if _, _, err := DecodeEdit(data); err != nil {
			b.Fatal(err)
		}
	}
}
