package ui

import (
	"fmt"
	"testing"
	"time"
)

// benchGrid is a rows x cols grid of the value kinds a page holds:
// integers, long strings, floats, timestamps, booleans with NULLs, and
// nested JSON.
func benchGrid(rows, cols int) grid {
	columns := make([]string, cols)
	for i := range columns {
		columns[i] = fmt.Sprintf("column_%d", i)
	}
	data := make([][]any, rows)
	for r := range data {
		row := make([]any, cols)
		for c := range row {
			switch c % 6 {
			case 0:
				row[c] = int64(r)
			case 1:
				row[c] = fmt.Sprintf("user-%d@example.org some long text value", r)
			case 2:
				row[c] = float64(r) * 1.25
			case 3:
				row[c] = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339)
			case 4:
				if r%3 == 0 {
					row[c] = nil
				} else {
					row[c] = true
				}
			case 5:
				row[c] = map[string]any{"id": r, "tags": []any{"a", "b"}}
			}
		}
		data[r] = row
	}
	return newGrid(columns, data)
}

// BenchmarkGridView is one frame of a 40-column grid at 200x50, with and
// without an applied search; newGrid runs outside the timer, as it does
// once per loaded page.
func BenchmarkGridView(b *testing.B) {
	for _, rows := range []int{500, 5000} {
		for _, query := range []string{"", "Example"} {
			g := benchGrid(rows, 40)
			g.cursorRow = rows / 2
			g.state.query = query
			b.Run(fmt.Sprintf("rows=%d/search=%t", rows, query != ""), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_ = g.view(200, 50, true)
				}
			})
		}
	}
}

// BenchmarkNewGrid is the one-time cost of building a grid for a page.
func BenchmarkNewGrid(b *testing.B) {
	for _, rows := range []int{500, 5000} {
		g := benchGrid(rows, 40)
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = newGrid(g.columns, g.rows)
			}
		})
	}
}
