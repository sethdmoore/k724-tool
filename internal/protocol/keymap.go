package protocol

// KeyboardLayout describes the physical key grid of the K724-RGB-PRO for the
// per-key colour editor. Each key's Index is its position in the 0x0b colour
// table / 0x09 remap table.
//
// The index->key mapping was read straight out of the 0x09 remap table in
// button_write_j_default_key.pcapng: every entry there is a 3-byte record whose
// third byte is the HID usage the key sends by default (0x29 Esc, 0x3a..0x45
// F1..F12, 0x14 Q, 0x04 A, and so on), so the table's order names each slot.
// write_light_a-r_s-g_d-b_q-w_e-bk.pcapng then confirmed the 0x0b colour table
// uses the identical order (A/S/D/Q/E -> entries 49/50/51/33/35).
//
// Slots with no key on this board's ANSI layout, and the four special records
// that are not plain keys (the volume knob at 13 and codes 0xa0xxxx at 88/89/
// 111/127), are omitted except Fn, which is included so its LED can be set.

// KeyCap is one key in KeyboardLayout.
type KeyCap struct {
	Name  string // display label
	Index int    // colour-table entry
	Units int    // width in quarter-units (4 == one standard key)
}

// gap is a spacer of the given quarter-unit width (Index -1, no LED).
func gap(units int) KeyCap { return KeyCap{Index: -1, Units: units} }

// KeyboardLayout is the editor grid, top row first.
var KeyboardLayout = [][]KeyCap{
	{
		{"Esc", 0, 4},
		{"F1", 1, 4}, {"F2", 2, 4}, {"F3", 3, 4}, {"F4", 4, 4},
		{"F5", 5, 4}, {"F6", 6, 4}, {"F7", 7, 4}, {"F8", 8, 4},
		{"F9", 9, 4}, {"F10", 10, 4}, {"F11", 11, 4}, {"F12", 12, 4},
		gap(2), {"Knob", 13, 4},
	},
	{
		{"`", 16, 4},
		{"1", 17, 4}, {"2", 18, 4}, {"3", 19, 4}, {"4", 20, 4}, {"5", 21, 4},
		{"6", 22, 4}, {"7", 23, 4}, {"8", 24, 4}, {"9", 25, 4}, {"0", 26, 4},
		{"-", 27, 4}, {"=", 28, 4}, {"Bksp", 29, 8},
		gap(2), {"Ins", 30, 4},
	},
	{
		{"Tab", 32, 6},
		{"Q", 33, 4}, {"W", 34, 4}, {"E", 35, 4}, {"R", 36, 4}, {"T", 37, 4},
		{"Y", 38, 4}, {"U", 39, 4}, {"I", 40, 4}, {"O", 41, 4}, {"P", 42, 4},
		{"[", 43, 4}, {"]", 44, 4}, {"\\", 45, 6},
		gap(2), {"Del", 46, 4},
	},
	{
		{"Caps", 48, 7},
		{"A", 49, 4}, {"S", 50, 4}, {"D", 51, 4}, {"F", 52, 4}, {"G", 53, 4},
		{"H", 54, 4}, {"J", 55, 4}, {"K", 56, 4}, {"L", 57, 4}, {";", 58, 4},
		{"'", 59, 4}, {"Enter", 61, 9},
		gap(2), {"PgUp", 62, 4},
	},
	{
		{"Shift", 64, 9},
		{"Z", 66, 4}, {"X", 67, 4}, {"C", 68, 4}, {"V", 69, 4}, {"B", 70, 4},
		{"N", 71, 4}, {"M", 72, 4}, {",", 73, 4}, {".", 74, 4}, {"/", 75, 4},
		{"Shift", 77, 7},
		gap(2), {"Up", 78, 4},
		gap(2), {"PgDn", 94, 4},
	},
	{
		{"Ctrl", 80, 5}, {"Win", 81, 5}, {"Alt", 82, 5},
		{"Space", 85, 25},
		{"Alt", 87, 5}, {"Fn", 88, 5}, {"Ctrl", 90, 5},
		gap(2),
		{"Left", 91, 4}, {"Down", 92, 4}, {"Right", 93, 4},
	},
}

// LayoutIndices returns every colour-table index the editor grid can address,
// in row-major order.
func LayoutIndices() []int {
	var out []int
	for _, row := range KeyboardLayout {
		for _, k := range row {
			if k.Index >= 0 {
				out = append(out, k.Index)
			}
		}
	}
	return out
}
