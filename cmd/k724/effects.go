package main

// lightingEffects maps a settings-block effect index (byte 1) to a display
// name. The names come from docs/RGB_LIGHTING.md; the byte values themselves
// were seen stepping 0x01..0x13 on the wire in light_presets.pcapng, so
// writing the index directly is safe even though the name mapping is a UI
// guess.
var lightingEffects = []struct {
	ID   byte
	Name string
}{
	{1, "Normal"},
	{2, "Breath"},
	{3, "Spectrum"},
	{4, "Traverse"},
	{5, "Rain"},
	{6, "Ripples"},
	{7, "Stars"},
	{8, "Reaction"},
	{9, "Stream"},
	{10, "Corrugated"},
	{11, "Cartoon"},
	{12, "Wave"},
	{13, "Serpentine"},
	{14, "Roll"},
	{15, "Flowers"},
	{16, "Scan"},
	{17, "Surmount"},
	{18, "Speed"},
	{19, "Custom"},
	{20, "Off"},
	{21, "Audio wave"},
	{22, "Light and shadow"},
}

func effectNames() []string {
	out := make([]string, len(lightingEffects))
	for i, e := range lightingEffects {
		out[i] = e.Name
	}
	return out
}

func effectIDForName(name string) (byte, bool) {
	for _, e := range lightingEffects {
		if e.Name == name {
			return e.ID, true
		}
	}
	return 0, false
}

func effectNameForID(id byte) string {
	for _, e := range lightingEffects {
		if e.ID == id {
			return e.Name
		}
	}
	return ""
}
