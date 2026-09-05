package styles

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Each bundled skin must load and visibly change the accent from the
// default (exact SGR values go through lipgloss's profile rounding, so we
// assert difference, not bytes).
func TestBundledSkinAccentsApply(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	seen := map[string]string{}
	for _, name := range BundledSkins() {
		if err := LoadSkin(name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := Header().Render("X")
		for prev, render := range seen {
			if render == got {
				t.Errorf("%s and %s render identical accents: %q", name, prev, got)
			}
		}
		seen[name] = got
	}
	if len(seen) != 3 {
		t.Fatalf("want 3 skins, got %d", len(seen))
	}
}
