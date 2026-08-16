package output

import (
	"fmt"
	"io"
	"strings"
)

// Table renders column-aligned human text using display widths rather than
// rune counts, so CJK wide runes align correctly (text/tabwriter pads by rune
// count and cannot). Cells are written verbatim — callers are responsible for
// sanitizing free-text cells. Rows are buffered until Flush so column widths
// can be computed from the whole table; columns are separated by two spaces.
type Table struct {
	w      io.Writer
	header []string
	rows   [][]string
	cols   int
	widths []int
}

// NewTable starts a table with the given header row (or nil header).
func NewTable(w io.Writer, header ...string) *Table {
	t := &Table{w: w, cols: len(header), header: header, widths: make([]int, len(header))}
	t.measure(header)
	return t
}

// Row appends one data row. The cell count must match the header width; a
// mismatch is a programming error and fails loudly rather than corrupting the
// alignment.
func (t *Table) Row(cells ...string) {
	if len(cells) != t.cols {
		panic(fmt.Sprintf("output.Table: row has %d cells, table has %d columns", len(cells), t.cols))
	}
	t.measure(cells)
	t.rows = append(t.rows, cells)
}

func (t *Table) measure(cells []string) {
	for i, cell := range cells {
		if w := DisplayWidth(cell); w > t.widths[i] {
			t.widths[i] = w
		}
	}
}

// Flush writes the buffered table and resets the table for reuse.
func (t *Table) Flush() error {
	if len(t.header) > 0 {
		if err := t.writeRow(t.header); err != nil {
			return err
		}
	}
	for _, row := range t.rows {
		if err := t.writeRow(row); err != nil {
			return err
		}
	}
	t.rows = t.rows[:0]
	t.widths = make([]int, t.cols)
	t.measure(t.header)
	return nil
}

func (t *Table) writeRow(cells []string) error {
	var b strings.Builder
	for i, cell := range cells {
		b.WriteString(cell)
		if i < len(cells)-1 {
			b.WriteString(strings.Repeat(" ", t.widths[i]-DisplayWidth(cell)+2))
		}
	}
	b.WriteString("\n")
	_, err := io.WriteString(t.w, b.String())
	return err
}
