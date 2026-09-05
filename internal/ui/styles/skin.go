package styles

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

//go:embed skins/*.yaml
var bundledSkins embed.FS

// BundledSkins lists the names shipped with f9s (F9S_SKIN=<name>).
func BundledSkins() []string {
	entries, _ := bundledSkins.ReadDir("skins")
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(out)
	return out
}

// skinColor is one token's value in skin.yaml: either a single color string
// (hex like "#E0607A" or an ANSI index like "203") or a light/dark pair.
type skinColor struct {
	value string
	light string
	dark  string
}

func (c *skinColor) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		c.value = node.Value
		return nil
	}
	var pair struct {
		Light string `yaml:"light"`
		Dark  string `yaml:"dark"`
	}
	if err := node.Decode(&pair); err != nil {
		return err
	}
	c.light, c.dark = pair.Light, pair.Dark
	return nil
}

func (c skinColor) terminalColor() lipgloss.TerminalColor {
	if c.value != "" {
		return lipgloss.Color(c.value)
	}
	return lipgloss.AdaptiveColor{Light: c.light, Dark: c.dark}
}

type skinFile struct {
	Colors map[string]skinColor `yaml:"colors"`
}

// DefaultSkinPath is ~/.config/f9s/skin.yaml, overridable via F9S_SKIN.
func DefaultSkinPath() string {
	if p := os.Getenv("F9S_SKIN"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "f9s", "skin.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "skin.yaml"
	}
	return filepath.Join(home, ".config", "f9s", "skin.yaml")
}

// LoadSkin overrides palette tokens from a skin: a bundled name
// (high-contrast, catppuccin, colorblind) or a yaml file path. A missing
// file keeps the defaults; an unknown token is an error so typos never
// fail silently.
func LoadSkin(path string) error {
	var data []byte
	var err error
	if !strings.ContainsAny(path, "/\\") { // bare name → try the bundle first
		if b, berr := bundledSkins.ReadFile("skins/" + strings.TrimSuffix(path, ".yaml") + ".yaml"); berr == nil {
			data = b
		}
	}
	if data == nil {
		data, err = os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	var f skinFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for token, color := range f.Colors {
		target, ok := tokenTargets[token]
		if !ok {
			return fmt.Errorf("%s: unknown token %q (valid: accent, ok, warn, error, dim, selectionFg, selectionBg)", path, token)
		}
		*target = color.terminalColor()
	}
	return nil
}
