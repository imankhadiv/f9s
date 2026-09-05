package styles

import "testing"

func TestBundledSkinsLoad(t *testing.T) {
	names := BundledSkins()
	if len(names) != 3 {
		t.Fatalf("want 3 bundled skins, got %v", names)
	}
	for _, n := range names {
		if err := LoadSkin(n); err != nil {
			t.Fatalf("bundled skin %q failed to load: %v", n, err)
		}
	}
	// a bare name that isn't bundled falls through to (missing) file → nil
	if err := LoadSkin("not-a-skin"); err != nil {
		t.Fatalf("unknown bare name should be a no-op, got %v", err)
	}
}
