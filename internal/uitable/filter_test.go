package uitable

import "testing"

func rowsOf(names ...string) [][]Cell {
	out := make([][]Cell, len(names))
	for i, n := range names {
		out[i] = []Cell{Plain(n), Plain("RUNNING")}
	}
	return out
}

func TestPowerFilter(t *testing.T) {
	m := New([]Column{{Title: "NAME"}, {Title: "STATE"}})
	m.SetRows(rowsOf("chaos_sink", "CarTopSpeed", "orders-agg", "orders-enrich"))

	cases := []struct {
		q    string
		want int
	}{
		{"orders", 2},         // substring
		{"!orders", 2},        // inverse: the other two
		{"^chaos", 1},         // regex anchor
		{"orders-(agg|x)", 1}, // regex alternation
		{"-f hos", 1},         // fuzzy: h..o..s → chaos_sink only (no 'h' in the others)
		{"zzzznope", 0},       // no match
	}
	for _, c := range cases {
		m.SetFilter(c.q)
		if got := m.Len(); got != c.want {
			t.Errorf("filter %q: got %d rows, want %d", c.q, got, c.want)
		}
	}
}
