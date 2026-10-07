package trame

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/citadellefr/trame/internal/charset"
	"github.com/citadellefr/trame/ot"
)

// Text is the format of plain text files: a single node, TextBody, whose
// text has one paragraph per line. It is written back with the byte order
// mark and the line endings it was read with, in UTF-8: a file that was not
// UTF-8 is read as Windows-1252, as Notepad does.
func Text(_ string, data []byte) (*ot.Tree, File, error) {
	var f textFile
	data, f.bom = bytes.CutPrefix(data, bom)
	text := string(data)
	if !utf8.Valid(data) {
		text = charset.Decode(data)
	}
	if i := strings.IndexByte(text, '\n'); i > 0 && text[i-1] == '\r' {
		f.crlf = true
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	doc, err := ot.NewTree(ot.Edit{{Op: ot.OpNew, ID: TextBody, Type: "text", Key: "V", Text: ot.Delta{{Insert: text + "\n"}}}})
	return doc, f, err
}

// TextBody is the node of a text file.
const TextBody = "body"

type textFile struct {
	bom  bool
	crlf bool
}

var (
	bom          = []byte{0xEF, 0xBB, 0xBF}
	errTextNodes = errors.New("a text file only has its text")
)

func (textFile) Check(_ *ot.Tree, e ot.Edit, _ Peer) error {
	for _, c := range e {
		if c.Op != ot.OpTxt || c.ID != TextBody {
			return errTextNodes
		}
	}
	return nil
}

func (f textFile) Encode(doc *ot.Tree) ([]byte, error) {
	var b bytes.Buffer
	b.Grow(doc.Len() + len(bom))
	if f.bom {
		b.Write(bom)
	}
	for i, p := range doc.Node(TextBody).Text.Paragraphs() {
		if i > 0 {
			if f.crlf {
				b.WriteByte('\r')
			}
			b.WriteByte('\n')
		}
		for j, o := range p {
			if j == len(p)-1 {
				o.Insert = strings.TrimSuffix(o.Insert, "\n")
			}
			b.WriteString(o.Insert)
		}
	}
	return b.Bytes(), nil
}
