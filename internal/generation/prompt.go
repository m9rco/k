package generation

import (
	"fmt"
	"regexp"
	"strings"
)

// EditKind enumerates the supported generation intents.
type EditKind string

const (
	EditCharacter  EditKind = "change_character"
	EditBackground EditKind = "change_background"
	EditText       EditKind = "change_text"
)

// Slots holds user-provided inputs in a structured form. User free text never
// becomes the prompt directly: each slot is sanitized and inserted into a
// server-controlled template (prompt-injection defense, design D5).
type Slots struct {
	Kind EditKind
	// CharacterDesc, BackgroundDesc, TextContent are the per-intent payloads.
	CharacterDesc  string
	BackgroundDesc string
	TextContent    string
	// ReuseComposition requests preserving the reference image's composition.
	ReuseComposition bool
}

// injectionPatterns match attempts to override system behavior. Matches are
// stripped from user-provided slot text before templating.
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions?`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)`),
	regexp.MustCompile(`(?i)\bsystem\s*:`),
	regexp.MustCompile(`(?i)\bassistant\s*:`),
	regexp.MustCompile(`(?i)you\s+are\s+now\b`),
	regexp.MustCompile(`(?i)forget\s+(everything|all)`),
	regexp.MustCompile(`(?i)new\s+instructions?\s*:`),
}

// maxSlotLen bounds each slot to keep prompts well-formed and limit abuse.
const maxSlotLen = 500

// Sanitize strips control-style injection patterns, collapses whitespace, and
// truncates to a safe length. The result is plain descriptive text safe to
// embed in a templated prompt.
func Sanitize(s string) string {
	for _, re := range injectionPatterns {
		s = re.ReplaceAllString(s, "")
	}
	// Drop control characters and collapse whitespace.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxSlotLen {
		s = s[:maxSlotLen]
	}
	return strings.TrimSpace(s)
}

// harmonyConstraint is the fixed color-adaptation clause (design D6). It is
// always appended so products stay tonally coherent regardless of the edit.
const harmonyConstraint = "Keep the overall color tone coherent with the source image; avoid abrupt or jarring color contrast. Match lighting and saturation to the original."

// BuildPrompt assembles the final generation prompt from sanitized slots and
// the extracted palette. The template is fully server-controlled; user text is
// only ever inserted as a sanitized descriptive fragment.
func BuildPrompt(slots Slots, palette []PaletteColor) (string, error) {
	var b strings.Builder

	switch slots.Kind {
	case EditCharacter:
		desc := Sanitize(slots.CharacterDesc)
		if desc == "" {
			return "", fmt.Errorf("character description required")
		}
		b.WriteString("Replace the main character in the image with: ")
		b.WriteString(desc)
		b.WriteString(". Preserve the existing scene and composition.")
	case EditBackground:
		desc := Sanitize(slots.BackgroundDesc)
		if desc == "" {
			return "", fmt.Errorf("background description required")
		}
		b.WriteString("Replace the background of the image with: ")
		b.WriteString(desc)
		b.WriteString(". Keep the foreground subject unchanged and well integrated.")
	case EditText:
		txt := Sanitize(slots.TextContent)
		if txt == "" {
			return "", fmt.Errorf("text content required")
		}
		b.WriteString("Replace the on-image text/copy with: \"")
		b.WriteString(txt)
		b.WriteString("\". Match the existing typographic style and placement.")
	default:
		return "", fmt.Errorf("unsupported edit kind %q", slots.Kind)
	}

	if slots.ReuseComposition {
		b.WriteString(" Reuse the reference image's composition and base elements; adapt the background to suit the new character.")
	}

	// Palette constraint.
	if len(palette) > 0 {
		hexes := make([]string, 0, len(palette))
		for _, c := range palette {
			hexes = append(hexes, c.Hex)
		}
		b.WriteString(" Harmonize with this color palette: ")
		b.WriteString(strings.Join(hexes, ", "))
		b.WriteString(".")
	}

	b.WriteString(" ")
	b.WriteString(harmonyConstraint)
	return b.String(), nil
}
