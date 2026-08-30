package protocol

import "testing"

// TestNavColumnAligned locks in docs/MISSING_FEATURES.md's "Nav column
// alignment" fix: Ins, Del, PgUp and PgDn must all start at the same
// cumulative unit offset within their row, so the editor grid draws them as
// one straight column.
func TestNavColumnAligned(t *testing.T) {
	want := map[string]bool{"Ins": true, "Del": true, "PgUp": true, "PgDn": true}
	var x0 int
	first := true
	for _, row := range KeyboardLayout {
		x := 0
		for _, k := range row {
			if want[k.Name] {
				if first {
					x0 = x
					first = false
				} else if x != x0 {
					t.Errorf("%s starts at unit %d, want %d (same column as the other nav keys)", k.Name, x, x0)
				}
			}
			x += k.Units
		}
	}
	if first {
		t.Fatal("no nav-column keys (Ins/Del/PgUp/PgDn) found in KeyboardLayout")
	}
}

// TestNoKnobEntry locks in the removal of the volume-knob placeholder: it
// has no LED, so KeyboardLayout should not carry an entry for it.
func TestNoKnobEntry(t *testing.T) {
	for _, row := range KeyboardLayout {
		for _, k := range row {
			if k.Name == "Knob" {
				t.Fatalf("KeyboardLayout still has a %q entry; the knob has no LED and should be omitted", k.Name)
			}
		}
	}
}
