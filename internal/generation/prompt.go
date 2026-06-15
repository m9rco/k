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
	// EditCharacterAdd ADDS a new character to the scene while keeping the
	// existing subject(s), as opposed to EditCharacter which REPLACES the main
	// character. Distinguishing the two prevents "增加一个角色" from being executed
	// as a replacement (the prompt template makes the add-vs-replace intent
	// explicit to the image model).
	EditCharacterAdd EditKind = "add_character"
	// EditIcon generates an icon related to the source image (re-creation via the
	// image model, not a pure crop). Output is converged to the target icon size
	// after generation (see service.run).
	EditIcon EditKind = "generate_icon"
	// EditTextToImage generates a brand-new image purely from a text description
	// (no source image). Used by the text-to-image capability (wan/qwen).
	EditTextToImage EditKind = "text_to_image"
)

// DefaultIconSize is the icon edge length used when the user gives no size.
const DefaultIconSize = 150

// Slots holds user-provided inputs in a structured form. User free text never
// becomes the prompt directly: each slot is sanitized and inserted into a
// server-controlled template (prompt-injection defense, design D5).
type Slots struct {
	Kind EditKind
	// CharacterDesc, BackgroundDesc, TextContent are the per-intent payloads.
	CharacterDesc  string
	BackgroundDesc string
	TextContent    string
	// IconDesc is the optional user hint for generate_icon (e.g. "扁平风格").
	IconDesc string
	// IconWidth/IconHeight are the desired icon dimensions for generate_icon.
	// Zero means use DefaultIconSize.
	IconWidth  int
	IconHeight int
	// TextToImageDesc is the user's scene description for text_to_image (no
	// source image). Sanitized before templating like every other slot.
	TextToImageDesc string
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
	case EditCharacterAdd:
		desc := Sanitize(slots.CharacterDesc)
		if desc == "" {
			return "", fmt.Errorf("character description required")
		}
		b.WriteString("Add a new character to the image while keeping the existing character(s) and subject unchanged: ")
		b.WriteString(desc)
		b.WriteString(". Place the new character naturally beside the existing one(s), preserving the original scene, composition and the existing subject; do NOT replace or remove anyone already in the image.")
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
	case EditIcon:
		// Icon generation derives a standalone app/game icon from the source. The
		// optional user hint is a sanitized descriptive fragment only.
		b.WriteString("Design a clean, standalone app/game icon derived from the main subject of the reference image. ")
		b.WriteString("Center the subject with balanced padding, bold and instantly recognizable at small sizes, ")
		b.WriteString("simple background (solid or transparent), no text unless essential.")
		if hint := Sanitize(slots.IconDesc); hint != "" {
			b.WriteString(" Style hint: ")
			b.WriteString(hint)
			b.WriteString(".")
		}
	case EditTextToImage:
		// Text-to-image: a brand-new image from a sanitized scene description. No
		// source image, so no palette/harmony clause is appended below.
		desc := Sanitize(slots.TextToImageDesc)
		if desc == "" {
			return "", fmt.Errorf("text-to-image description required")
		}
		b.WriteString("Create a high-quality marketing illustration based on this description: ")
		b.WriteString(desc)
		b.WriteString(". Coherent composition, balanced lighting, polished and production-ready.")
		return b.String(), nil
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
