package ot

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// A Grid is the cells of a sheet: sparse, addressed by row and column
// counted from 1, each cell a JSON object of fields. Row 0 holds the
// fields of whole columns, column 0 those of whole rows, so that inserting
// or removing rows and columns moves them along with the cells.
//
// Setting a field is last-writer-wins, like a node's attributes. Inserting
// and removing rows or columns shifts what comes after, and a cell set on a
// row that someone removed meanwhile is dropped with it.
type Grid struct {
	rows  []int32
	cells [][]gridCell // of each row, by column; never changed in place
	size  int
}

type gridCell struct {
	col    int32
	fields string
}

// Cell is a cell in a change: the fields it sets, a null removing one.
// It is written [row, column, {fields}].
type Cell struct {
	Row, Col int
	Fields   json.RawMessage
}

// The size of a sheet in Excel.
const (
	MaxRows = 1 << 20
	MaxCols = 1 << 14
)

const (
	OpCel = "cel"
	OpIns = "ins"
	OpRem = "rem"
)

// Dimensions of Change.Dim.
const (
	DimRows = "r"
	DimCols = "c"
)

func (c Cell) MarshalJSON() ([]byte, error) {
	b := []byte{'['}
	b = strconv.AppendInt(b, int64(c.Row), 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, int64(c.Col), 10)
	b = append(b, ',')
	b = append(b, c.Fields...)
	return append(b, ']'), nil
}

func (c *Cell) UnmarshalJSON(data []byte) error {
	var blank bool
	i := skipBlank(data, 0, &blank)
	if i >= len(data) || data[i] != '[' {
		return ErrInvalid
	}
	var ok bool
	if c.Row, i, ok = scanIndex(data, i+1); !ok {
		return ErrInvalid
	}
	if c.Col, i, ok = scanIndex(data, i); !ok {
		return ErrInvalid
	}
	start := skipBlank(data, i, &blank)
	end, ok := scanValue(data, start, 0, &blank)
	if !ok {
		return ErrInvalid
	}
	if i = skipBlank(data, end, &blank); i >= len(data) || data[i] != ']' || skipBlank(data, i+1, &blank) != len(data) {
		return ErrInvalid
	}
	c.Fields = bytes.Clone(data[start:end])
	return nil
}

// scanIndex reads an integer at i, then the comma after it, and returns it
// with the offset after the comma.
func scanIndex(data []byte, i int) (n, next int, ok bool) {
	var blank bool
	i = skipBlank(data, i, &blank)
	end, ok := scanNumber(data, i)
	if !ok {
		return 0, 0, false
	}
	n64, err := strconv.ParseInt(string(data[i:end]), 10, 0)
	if err != nil {
		return 0, 0, false
	}
	i = skipBlank(data, end, &blank)
	if i >= len(data) || data[i] != ',' {
		return 0, 0, false
	}
	return int(n64), i + 1, true
}

// fields reads a cell's object; set allows the nulls that remove fields.
func fields(raw json.RawMessage, set bool) (map[string]json.RawMessage, bool) {
	var f map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &f) != nil || f == nil {
		return nil, false
	}
	for k, v := range f {
		if k == "" || !set && bytes.Equal(v, null) {
			return nil, false
		}
	}
	return f, true
}

func checkCells(cells []Cell, set bool) bool {
	var seen map[[2]int]bool
	for i, c := range cells {
		if c.Row < 0 || c.Row > MaxRows || c.Col < 0 || c.Col > MaxCols {
			return false
		}
		if i > 0 && seen == nil && compareCells(cells[i-1], c) >= 0 {
			seen = make(map[[2]int]bool, len(cells))
			for _, d := range cells[:i] {
				seen[[2]int{d.Row, d.Col}] = true
			}
		}
		if seen != nil {
			at := [2]int{c.Row, c.Col}
			if seen[at] {
				return false
			}
			seen[at] = true
		}
		n, _, ok := scanFields(c.Fields, set)
		if !ok || set && n == 0 {
			return false
		}
	}
	return true
}

// scanFields checks that raw is a cell's object, set allowing the nulls
// that remove fields, and counts its fields. canonical tells whether it is
// written as compact writes it: no blanks, keys in order, nothing escaped.
func scanFields(raw []byte, set bool) (n int, canonical, ok bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return 0, false, false
	}
	canonical = true
	var last []byte
	i := skipBlank(raw, 1, &canonical)
	for n == 0 && i < len(raw) && raw[i] != '}' || n > 0 && i < len(raw) && raw[i] == ',' {
		if n > 0 {
			i = skipBlank(raw, i+1, &canonical)
		}
		if i >= len(raw) || raw[i] != '"' {
			return 0, false, false
		}
		end, ok := scanString(raw, i, &canonical)
		if !ok || end-i == 2 {
			return 0, false, false
		}
		key := raw[i+1 : end-1]
		if bytes.IndexByte(key, '\\') >= 0 || last != nil && bytes.Compare(last, key) >= 0 {
			canonical = false
		}
		last = key
		i = skipBlank(raw, end, &canonical)
		if i >= len(raw) || raw[i] != ':' {
			return 0, false, false
		}
		i = skipBlank(raw, i+1, &canonical)
		start := i
		if i, ok = scanValue(raw, i, 0, &canonical); !ok || !set && bytes.Equal(raw[start:i], null) {
			return 0, false, false
		}
		n++
		i = skipBlank(raw, i, &canonical)
	}
	if i >= len(raw) || raw[i] != '}' || skipBlank(raw, i+1, &canonical) != len(raw) {
		return 0, false, false
	}
	return n, canonical, true
}

func skipBlank(raw []byte, i int, canonical *bool) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		*canonical = false
		i++
	}
	return i
}

// maxNesting is how deep values nest, as encoding/json allows.
const maxNesting = 10000

// scanValue is the end of the JSON value raw holds at i, and whether it
// is valid.
func scanValue(raw []byte, i, depth int, canonical *bool) (int, bool) {
	if i >= len(raw) || depth > maxNesting {
		return i, false
	}
	switch c := raw[i]; c {
	case '"':
		return scanString(raw, i, canonical)
	case '{', '[':
		end := byte(']')
		if c == '{' {
			end = '}'
		}
		i = skipBlank(raw, i+1, canonical)
		if i < len(raw) && raw[i] == end {
			return i + 1, true
		}
		for {
			var ok bool
			if c == '{' {
				if i >= len(raw) || raw[i] != '"' {
					return i, false
				}
				if i, ok = scanString(raw, i, canonical); !ok {
					return i, false
				}
				if i = skipBlank(raw, i, canonical); i >= len(raw) || raw[i] != ':' {
					return i, false
				}
				i = skipBlank(raw, i+1, canonical)
			}
			if i, ok = scanValue(raw, i, depth+1, canonical); !ok {
				return i, false
			}
			if i = skipBlank(raw, i, canonical); i >= len(raw) {
				return i, false
			}
			if raw[i] == end {
				return i + 1, true
			}
			if raw[i] != ',' {
				return i, false
			}
			i = skipBlank(raw, i+1, canonical)
		}
	case 't':
		return literal(raw, i, "true")
	case 'f':
		return literal(raw, i, "false")
	case 'n':
		return literal(raw, i, "null")
	}
	return scanNumber(raw, i)
}

func literal(raw []byte, i int, word string) (int, bool) {
	end := i + len(word)
	return end, end <= len(raw) && string(raw[i:end]) == word
}

// scanString is the end of the string raw holds at i, and whether it is
// valid. What compact would escape or replace, markup for HTML, the line
// separators of JavaScript and invalid UTF-8, makes it not canonical.
func scanString(raw []byte, i int, canonical *bool) (int, bool) {
	for i++; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '"':
			return i + 1, true
		case c < 0x20:
			return i, false
		case c == '<' || c == '>' || c == '&':
			*canonical = false
		case c >= utf8.RuneSelf:
			r, size := utf8.DecodeRune(raw[i:])
			if r == utf8.RuneError && size == 1 || r == '\u2028' || r == '\u2029' {
				*canonical = false
			}
			i += size - 1
		case c == '\\':
			if i++; i >= len(raw) {
				return i, false
			}
			switch raw[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				if i+4 >= len(raw) {
					return i, false
				}
				for _, h := range raw[i+1 : i+5] {
					if !('0' <= h && h <= '9' || 'a' <= h && h <= 'f' || 'A' <= h && h <= 'F') {
						return i, false
					}
				}
				i += 4
			default:
				return i, false
			}
		}
	}
	return i, false
}

func scanNumber(raw []byte, i int) (int, bool) {
	digits := func() int {
		start := i
		for i < len(raw) && '0' <= raw[i] && raw[i] <= '9' {
			i++
		}
		return i - start
	}
	if i < len(raw) && raw[i] == '-' {
		i++
	}
	switch {
	case i < len(raw) && raw[i] == '0':
		i++
	case digits() == 0:
		return i, false
	}
	if i < len(raw) && raw[i] == '.' {
		i++
		if digits() == 0 {
			return i, false
		}
	}
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		i++
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			i++
		}
		if digits() == 0 {
			return i, false
		}
	}
	return i, true
}

// newGrid is a grid of cells Check accepted, which only set fields, each
// once. Their cells share one array and their fields one string.
func newGrid(cells []Cell) (*Grid, error) {
	if !slices.IsSortedFunc(cells, compareCells) {
		cells = slices.Clone(cells)
		slices.SortFunc(cells, compareCells)
	}
	var text strings.Builder
	size := 0
	for _, c := range cells {
		size += len(c.Fields)
	}
	text.Grow(size)
	all := make([]gridCell, 0, len(cells))
	var rows []int32
	var ends, firsts []int
	for i, c := range cells {
		n, canonical, ok := scanFields(c.Fields, false)
		if !ok || i > 0 && compareCells(cells[i-1], c) == 0 {
			return nil, ErrInvalid
		}
		if n == 0 {
			continue
		}
		if canonical {
			text.Write(c.Fields)
		} else {
			m, _ := fields(c.Fields, false)
			text.WriteString(compact(m))
		}
		ends = append(ends, text.Len())
		if k := len(rows); k == 0 || rows[k-1] != int32(c.Row) {
			rows = append(rows, int32(c.Row))
			firsts = append(firsts, len(all))
		}
		all = append(all, gridCell{col: int32(c.Col)})
	}
	fields := text.String()
	start := 0
	for i, end := range ends {
		all[i].fields = fields[start:end]
		start = end
	}
	g := &Grid{rows: rows, cells: make([][]gridCell, len(rows)), size: len(all)}
	for i, first := range firsts {
		last := len(all)
		if i+1 < len(firsts) {
			last = firsts[i+1]
		}
		g.cells[i] = all[first:last:last]
	}
	return g, nil
}

func compareCells(a, b Cell) int {
	if a.Row != b.Row {
		return a.Row - b.Row
	}
	return a.Col - b.Col
}

// Len is the number of cells.
func (g *Grid) Len() int {
	return g.size
}

// raw is fields as JSON without copying them: what the grid hands out is
// read, never modified.
func raw(fields string) json.RawMessage {
	return unsafe.Slice(unsafe.StringData(fields), len(fields))
}

// Cell is the fields of a cell, nil when it has none. The fields a grid
// gives must not be modified.
func (g *Grid) Cell(row, col int) json.RawMessage {
	i, ok := slices.BinarySearch(g.rows, int32(row))
	if !ok {
		return nil
	}
	cells := g.cells[i]
	j, ok := slices.BinarySearchFunc(cells, int32(col), func(c gridCell, col int32) int { return int(c.col - col) })
	if !ok {
		return nil
	}
	return raw(cells[j].fields)
}

// Cells are all the cells, row by row.
func (g *Grid) Cells() []Cell {
	out := make([]Cell, 0, g.size)
	for i, r := range g.rows {
		for _, c := range g.cells[i] {
			out = append(out, Cell{Row: int(r), Col: int(c.col), Fields: raw(c.fields)})
		}
	}
	return out
}

// Each calls f with every cell of rows from lo to hi, in order, until f
// returns false.
func (g *Grid) Each(lo, hi int, f func(row, col int, fields json.RawMessage) bool) {
	i, _ := slices.BinarySearch(g.rows, int32(lo))
	for ; i < len(g.rows) && int(g.rows[i]) <= hi; i++ {
		for _, c := range g.cells[i] {
			if !f(int(g.rows[i]), int(c.col), raw(c.fields)) {
				return
			}
		}
	}
}

// EachIn calls f with every cell of rows r1 to r2 and columns c1 to c2,
// row by row, until f returns false.
func (g *Grid) EachIn(r1, r2, c1, c2 int, f func(row, col int, fields json.RawMessage) bool) {
	i, _ := slices.BinarySearch(g.rows, int32(r1))
	for ; i < len(g.rows) && int(g.rows[i]) <= r2; i++ {
		cells := g.cells[i]
		j, _ := slices.BinarySearchFunc(cells, int32(c1), func(c gridCell, col int32) int { return int(c.col - col) })
		for ; j < len(cells) && int(cells[j].col) <= c2; j++ {
			if !f(int(g.rows[i]), int(cells[j].col), raw(cells[j].fields)) {
				return
			}
		}
	}
}

// Bounds are the last row and the last column holding cells.
func (g *Grid) Bounds() (rows, cols int) {
	if len(g.rows) == 0 {
		return 0, 0
	}
	for _, cells := range g.cells {
		cols = max(cols, int(cells[len(cells)-1].col))
	}
	return int(g.rows[len(g.rows)-1]), cols
}

// Clone is a copy that later edits of g leave alone.
func (g *Grid) Clone() *Grid {
	return &Grid{rows: slices.Clone(g.rows), cells: slices.Clone(g.cells), size: g.size}
}

// put sets the fields of a cell, "" removing it.
func (g *Grid) put(row, col int, f string) {
	i, found := slices.BinarySearch(g.rows, int32(row))
	if !found {
		if f == "" {
			return
		}
		g.rows = slices.Insert(g.rows, i, int32(row))
		g.cells = slices.Insert(g.cells, i, nil)
	}
	cells := g.cells[i]
	j, ok := slices.BinarySearchFunc(cells, int32(col), func(c gridCell, col int32) int { return int(c.col - col) })
	switch {
	case ok && f == "":
		cells = slices.Delete(slices.Clone(cells), j, j+1)
		g.size--
	case ok:
		cells = slices.Clone(cells)
		cells[j].fields = f
	case f != "":
		cells = slices.Insert(slices.Clone(cells), j, gridCell{int32(col), f})
		g.size++
	}
	if len(cells) == 0 {
		g.rows = slices.Delete(g.rows, i, i+1)
		g.cells = slices.Delete(g.cells, i, i+1)
		return
	}
	g.cells[i] = cells
}

func (g *Grid) set(cells []Cell) {
	for _, c := range cells {
		if _, canonical, _ := scanFields(c.Fields, true); canonical {
			g.put(c.Row, c.Col, merge(g.Cell(c.Row, c.Col), c.Fields))
			continue
		}
		set, _ := fields(c.Fields, true)
		old, _ := fields(g.Cell(c.Row, c.Col), false)
		if old == nil {
			old = map[string]json.RawMessage{}
		}
		for k, v := range set {
			if bytes.Equal(v, null) {
				delete(old, k)
			} else {
				old[k] = v
			}
		}
		if len(old) == 0 {
			g.put(c.Row, c.Col, "")
		} else {
			g.put(c.Row, c.Col, compact(old))
		}
	}
}

// merge is the fields of a cell once set sets some, both written as
// compact writes them, so that neither needs decoding.
func merge(old, set []byte) string {
	b := make([]byte, 0, len(old)+len(set))
	b = append(b, '{')
	add := func(key, val []byte) {
		if bytes.Equal(val, null) {
			return
		}
		if len(b) > 1 {
			b = append(b, ',')
		}
		b = append(b, key...)
		b = append(b, ':')
		b = append(b, val...)
	}
	a, z := members(old), members(set)
	for len(a) > 0 || len(z) > 0 {
		switch {
		case len(z) == 0 || len(a) > 0 && bytes.Compare(a[0][0], z[0][0]) < 0:
			add(a[0][0], a[0][1])
			a = a[1:]
		case len(a) == 0 || bytes.Compare(z[0][0], a[0][0]) < 0:
			add(z[0][0], z[0][1])
			z = z[1:]
		default:
			add(z[0][0], z[0][1])
			a, z = a[1:], z[1:]
		}
	}
	if len(b) == 1 {
		return ""
	}
	return string(append(b, '}'))
}

// members are the keys, quoted, and values of an object written as
// compact writes it.
func members(raw []byte) [][2][]byte {
	var out [][2][]byte
	canonical := true
	for i := 1; i < len(raw) && raw[i] == '"'; {
		end, _ := scanString(raw, i, &canonical)
		v, _ := scanValue(raw, end+1, 0, &canonical)
		out = append(out, [2][]byte{raw[i:end], raw[end+1 : v]})
		i = v + 1
	}
	return out
}

func compact(f map[string]json.RawMessage) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// shift inserts n rows or columns at at, or removes them when n is
// negative; what is pushed past the end of the sheet is dropped.
func (g *Grid) shift(dim string, at, n int) {
	if dim == DimRows {
		var rows []int32
		var cells [][]gridCell
		for i, r := range g.rows {
			m, ok := moved(int(r), at, n, MaxRows)
			if !ok {
				g.size -= len(g.cells[i])
				continue
			}
			rows = append(rows, int32(m))
			cells = append(cells, g.cells[i])
		}
		g.rows, g.cells = rows, cells
		return
	}
	for i, row := range g.cells {
		changed := false
		out := make([]gridCell, 0, len(row))
		for _, c := range row {
			m, ok := moved(int(c.col), at, n, MaxCols)
			if !ok {
				g.size--
				changed = true
				continue
			}
			changed = changed || m != int(c.col)
			out = append(out, gridCell{int32(m), c.fields})
		}
		if changed {
			g.cells[i] = out
		}
	}
	g.dropEmptyRows()
}

func (g *Grid) dropEmptyRows() {
	var rows []int32
	var cells [][]gridCell
	for i, r := range g.rows {
		if len(g.cells[i]) > 0 {
			rows = append(rows, r)
			cells = append(cells, g.cells[i])
		}
	}
	g.rows, g.cells = rows, cells
}

// moved is where index i goes when n rows or columns are inserted at at,
// or removed when n is negative, and whether it is still there.
func moved(i, at, n, limit int) (int, bool) {
	switch {
	case i < at:
		return i, true
	case n < 0 && i < at-n:
		return 0, false
	case i+n > limit:
		return 0, false
	}
	return i + n, true
}

// transformGrid rebases two changes to the same grid over each other; a
// change left with nothing to do has no Op.
func transformGrid(a, b Change, aFirst bool) (Change, Change) {
	switch {
	case a.Op == OpCel && b.Op == OpCel:
		if aFirst {
			a.Cells = overriddenCells(a.Cells, b.Cells)
		} else {
			b.Cells = overriddenCells(b.Cells, a.Cells)
		}
	case a.Op == OpCel:
		a.Cells = shiftCells(a.Cells, b)
	case b.Op == OpCel:
		b.Cells = shiftCells(b.Cells, a)
	case a.Dim != b.Dim:
	case a.Op == OpIns && b.Op == OpIns:
		if a.At < b.At || a.At == b.At && aFirst {
			b.At += a.N
		} else {
			a.At += b.N
		}
	case a.Op == OpRem && b.Op == OpRem:
		a, b = removedOver(a, b), removedOver(b, a)
	case a.Op == OpIns:
		b, a = removalOverInsert(b, a)
	default:
		a, b = removalOverInsert(a, b)
	}
	for _, c := range []*Change{&a, &b} {
		if c.Op == OpCel && len(c.Cells) == 0 || (c.Op == OpIns || c.Op == OpRem) && c.N <= 0 {
			c.Op = ""
		}
	}
	return a, b
}

// overriddenCells are cells without the fields later sets.
func overriddenCells(cells, later []Cell) []Cell {
	sets := make(map[[2]int]map[string]json.RawMessage, len(later))
	for _, c := range later {
		sets[[2]int{c.Row, c.Col}], _ = fields(c.Fields, true)
	}
	var out []Cell
	for _, c := range cells {
		set := sets[[2]int{c.Row, c.Col}]
		if set == nil {
			out = append(out, c)
			continue
		}
		f, _ := fields(c.Fields, true)
		for k := range set {
			delete(f, k)
		}
		if len(f) > 0 {
			out = append(out, Cell{c.Row, c.Col, json.RawMessage(compact(f))})
		}
	}
	return out
}

// shiftCells moves cells where rows or columns inserted or removed by s
// put them, dropping those removed.
func shiftCells(cells []Cell, s Change) []Cell {
	n := s.N
	if s.Op == OpRem {
		n = -n
	}
	var out []Cell
	for _, c := range cells {
		var ok bool
		if s.Dim == DimRows {
			c.Row, ok = moved(c.Row, s.At, n, MaxRows)
		} else {
			c.Col, ok = moved(c.Col, s.At, n, MaxCols)
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// removedOver is the removal r once other is removed too.
func removedOver(r, other Change) Change {
	end, otherEnd := r.At+r.N, other.At+other.N
	before := max(0, min(otherEnd, r.At)-other.At)
	overlap := max(0, min(end, otherEnd)-max(r.At, other.At))
	r.At -= before
	r.N -= overlap
	return r
}

// removalOverInsert rebases a removal and an insertion over each other.
// Rows inserted inside the removed ones are removed with them.
func removalOverInsert(rem, ins Change) (Change, Change) {
	switch {
	case ins.At <= rem.At:
		rem.At += ins.N
	case ins.At >= rem.At+rem.N:
		ins.At -= rem.N
	default:
		rem.N += ins.N
		ins.N = 0
	}
	return rem, ins
}
