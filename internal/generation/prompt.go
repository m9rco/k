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
	// EditAdaptPlatform re-composes a source image to fit a target platform size
	// (横竖翻转 / 比例差异大时由智能路由选中)。It is NOT a pure crop: the model
	// repaints/extends the scene to the new aspect ratio while preserving the
	// subject and core marketing intent. Output is converged to the exact target
	// size after generation (see service.run, same范式 as EditIcon).
	EditAdaptPlatform EditKind = "adapt_platform"
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
	// --- platform adaptation (EditAdaptPlatform) ---
	// These describe the target placement so the template can express the
	// platform-adaptation intent in model-agnostic terms. ChannelName /
	// AssetTypeName / SizeNote are catalog-sourced strings; they are Sanitized
	// before templating (they originate from server config, not user free text,
	// but sanitizing keeps the template uniformly injection-safe). AdaptDesc is
	// the optional user hint and is always Sanitized.
	ChannelName   string
	AssetTypeKey  string // catalog type key: icon / cover / banner / screenshot / video / h5 / …
	AssetTypeName string
	Orientation   string // landscape / portrait / square
	TargetWidth   int
	TargetHeight  int
	SizeNote      string // e.g. 无文案 / 仅 logo / 圆角 / 透明底 / 安全区
	AdaptDesc     string // optional user description for the adaptation
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
	case EditAdaptPlatform:
		b.WriteString("Adapt this marketing image for a new platform placement. ")
		b.WriteString("Keep the main subject/characters and the core marketing intent fully intact — do NOT crop the subject out, omit, or alter the key visual message. ")
		// Per-asset-type composition guidance so the model generates the RIGHT
		// kind of asset, not just a resized version of the source.
		if guide := assetTypeGuide(slots.AssetTypeKey); guide != "" {
			b.WriteString(guide)
			b.WriteString(" ")
		}
		if placement := buildPlacementPhrase(slots); placement != "" {
			b.WriteString("Recompose for ")
			b.WriteString(placement)
			b.WriteString(". ")
		}
		b.WriteString("Re-frame and extend/repaint the scene and background to fill the new aspect ratio naturally, rather than cropping; reposition the subject for a balanced composition at the target proportions. ")
		if note := Sanitize(slots.SizeNote); note != "" {
			b.WriteString("Respect this placement constraint: ")
			b.WriteString(note)
			b.WriteString(". ")
		}
		if hint := Sanitize(slots.AdaptDesc); hint != "" {
			b.WriteString("Additional direction: ")
			b.WriteString(hint)
			b.WriteString(". ")
		}
		b.WriteString("Production-ready, polished result.")
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

// buildPlacementPhrase composes the sanitized "<orientation> WxH <channel>
// <assetType> placement" fragment for the platform-adaptation template. Every
// piece is optional and Sanitized; missing pieces are simply omitted so the
// phrase stays well-formed (e.g. with only dimensions: "a 1080×1920 placement").
func buildPlacementPhrase(slots Slots) string {
	var parts []string
	if o := Sanitize(slots.Orientation); o != "" {
		parts = append(parts, o)
	}
	if slots.TargetWidth > 0 && slots.TargetHeight > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", slots.TargetWidth, slots.TargetHeight))
	}
	if ch := Sanitize(slots.ChannelName); ch != "" {
		parts = append(parts, ch)
	}
	if at := Sanitize(slots.AssetTypeName); at != "" {
		parts = append(parts, at)
	}
	if len(parts) == 0 {
		return ""
	}
	return "a " + strings.Join(parts, " ") + " placement"
}

// assetTypeGuide returns composition instructions specific to the asset type so
// the image model produces the RIGHT kind of asset — not just a resized source.
// Keys match the catalog's AssetType.Type values (channels.json).
func assetTypeGuide(key string) string {
	switch key {
	case "icon":
		return "Generate a clean app/game icon: center the main subject with balanced padding, bold and instantly recognizable at small sizes, simple or transparent background, icon-style composition."
	case "cover":
		return "Generate a cover image: place the focal subject prominently in the upper-center area leaving breathing room, leave the lower portion for potential title/copy overlay, cinematic framing."
	case "banner":
		return "Generate a horizontal banner: wide promotional composition, subject on one side with open space for copy on the other, strong visual hierarchy, suitable for advertising placement."
	case "screenshot":
		return "Generate a screenshot-style promotional image: show the key game scene or UI moment clearly, realistic in-game framing, highlight the core gameplay or visual appeal."
	case "video":
		return "Generate a video thumbnail/cover: strong focal subject, high contrast, thumbnail-optimized composition that reads well at small sizes."
	default:
		return ""
	}
}
