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
	// GenWidth/GenHeight are the resolved generation dimensions passed to the
	// image provider (after resolveGptImage2Size snapping). When set, they
	// replace TargetWidth/TargetHeight in the placement phrase so the model
	// generates at full quality rather than inferring a tiny output size from
	// a small target (e.g. 200×200 icon → tell the model 1728×1728 so it
	// generates full detail, then we converge down to exact during crop).
	GenWidth  int
	GenHeight int
	SizeNote  string // e.g. 无文案 / 仅 logo / 圆角 / 透明底 / 安全区
	AdaptDesc string // optional user description for the adaptation
	// --- harness inputs set by the service (not user free text) ---
	// RefCount is how many reference images this generation feeds the model
	// (anchor + auxiliaries). When ≥2 the prompt adds an explicit anchor-role
	// clause so the model treats the first image as the sole source of truth and
	// the rest as style/element hints only (design D2/D3). 0/1 omits it.
	RefCount int
	// ProviderSupportsTransparency reports whether the resolved image adapter can
	// produce a real transparent background. gpt-image-2 cannot, so a 透明底 size
	// note is rewritten to a clean-cutout phrasing instead of being injected
	// verbatim (which would make the model "paint" a fake checkerboard). Gemini
	// and similar adapters set this true and keep the constraint as-is (design D4).
	ProviderSupportsTransparency bool
	// ThemeReport is a server-generated marketing analysis report (produced by
	// the vision analysis stage). When set, a THEME segment is injected between
	// PRESERVE and MODIFY so the image model can anchor to the analyzed subject
	// and must-preserve elements. The report is Sanitized before templating.
	// Only set for adapt_platform when COS upload + vision analysis are available.
	ThemeReport string
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
// Counted in runes (characters), not bytes, so CJK text gets the same character
// budget as ASCII and truncation never splits a multi-byte rune (which would
// emit a U+FFFD "�" replacement char into the prompt).
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
	// Truncate on a rune boundary so a multi-byte character (e.g. a Chinese
	// glyph) is never cut in half into an invalid "�" sequence.
	if r := []rune(s); len(r) > maxSlotLen {
		s = string(r[:maxSlotLen])
	}
	return strings.TrimSpace(s)
}

// The low-divergence prompt harness (design D3) wraps every image-edit intent in
// four server-controlled segments so "stay in the game, stay harmonious, do not
// hallucinate" is encoded as a checklist the model executes rather than relying on
// scattered phrasing. CONTEXT / PRESERVE / AVOID are fixed text and never accept
// user input (injection-safe); MODIFY carries the per-intent instruction plus the
// sanitized user slot.
const (
	// contextClause front-loads the marketing domain so the model treats the
	// reference as authentic game key art to extend — not a blank canvas to
	// reinvent. Directly targets "脱离游戏本身 / 虚构游戏画面".
	contextClause = "CONTEXT: This is a promotional marketing asset for an existing video game. The reference image(s) are authentic key art / in-game visuals from that game. Stay strictly within the game's established art style, world, characters and tone. Do NOT invent gameplay, UI, scenes, characters or worlds not present in the references, and do NOT turn it into generic fantasy or stock illustration."

	// preserveClause locks the anchor's identity and core message.
	preserveClause = "PRESERVE: Keep the main subject/characters' identity (appearance, outfit, signature features), the core marketing message, and the key visual elements faithful to the reference; stay true to its intent."

	// anchorClause is appended to PRESERVE only with ≥2 references, naming the
	// first image as the single source of truth (design D2).
	anchorClause = "The FIRST reference image is the anchor — the single source of truth for subject and intent; the other references contribute style, palette and element inspiration only and must NOT replace the anchor's subject."

	// avoidClause is the negative fence against the known divergence modes of
	// multi-image edits.
	avoidClause = "AVOID: inventing new subjects, scenes or anything not in the game; changing the characters' identity; adding text, watermarks or logos absent from the anchor; letting auxiliary references override the anchor subject. Keep the overall color tone, lighting and saturation coherent with the reference; avoid abrupt or jarring contrast."

	// textToImageContext is the lighter domain framing for source-less generation
	// (no anchor to preserve, so no PRESERVE/anchor clause).
	textToImageContext = "CONTEXT: Produce a promotional marketing illustration for a video game. Keep it coherent and production-ready; do not add unrequested text, watermarks or logos."
)

// transparencyRewrite replaces a 透明底/透明背景 size note when the resolved
// adapter cannot produce real transparency (gpt-image-2). Asking such a model for
// a transparent background makes it paint a fake one; a clean-cutout phrasing is
// honored instead, and the exact transparency is the post-processing step's job.
const transparencyRewrite = "纯净中性单色背景，主体边缘干净清晰便于后期抠图"

// rewriteSizeNote applies capability-aware rewrites to a catalog size note before
// it is injected. Currently only the transparent-background case (design D4); all
// other notes (无文案/仅 logo/圆角/安全区) pass through unchanged.
func rewriteSizeNote(note string, supportsTransparency bool) string {
	if note == "" {
		return ""
	}
	if !supportsTransparency && (strings.Contains(note, "透明底") || strings.Contains(note, "透明背景")) {
		return transparencyRewrite
	}
	return note
}

// BuildPrompt assembles the final generation prompt from sanitized slots and
// the extracted palette. The template is fully server-controlled; user text is
// only ever inserted as a sanitized descriptive fragment. Image-edit intents are
// wrapped in the four-segment low-divergence harness (CONTEXT/PRESERVE/MODIFY/
// AVOID); text-to-image gets only the lighter CONTEXT framing.
func BuildPrompt(slots Slots, palette []PaletteColor) (string, error) {
	// modify accumulates the per-intent instruction (the MODIFY segment); the
	// surrounding CONTEXT/PRESERVE/AVOID segments are added once at the end.
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
		// source image, so only the lighter CONTEXT framing is added (no anchor to
		// preserve, no palette/harmony clause).
		desc := Sanitize(slots.TextToImageDesc)
		if desc == "" {
			return "", fmt.Errorf("text-to-image description required")
		}
		var t strings.Builder
		t.WriteString(textToImageContext)
		t.WriteString(" Create a high-quality marketing illustration based on this description: ")
		t.WriteString(desc)
		t.WriteString(". Coherent composition, balanced lighting, polished and production-ready.")
		return t.String(), nil
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
		if hint := extremeRatioHint(slots.TargetWidth, slots.TargetHeight); hint != "" {
			b.WriteString(hint)
			b.WriteString(" ")
		}
		b.WriteString("Re-frame and extend/repaint the scene and background to fill the new aspect ratio naturally, rather than cropping; reposition the subject for a balanced composition at the target proportions. ")
		if note := rewriteSizeNote(Sanitize(slots.SizeNote), slots.ProviderSupportsTransparency); note != "" {
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

	// Palette constraint (anchor-derived colors to harmonize with).
	if len(palette) > 0 {
		hexes := make([]string, 0, len(palette))
		for _, c := range palette {
			hexes = append(hexes, c.Hex)
		}
		b.WriteString(" Harmonize with this color palette: ")
		b.WriteString(strings.Join(hexes, ", "))
		b.WriteString(".")
	}

	// Wrap the per-intent MODIFY body in the four-segment harness. PRESERVE gains
	// the anchor-role clause when multiple references are in play.
	preserve := preserveClause
	if slots.RefCount >= 2 {
		preserve = preserveClause + " " + anchorClause
	}
	theme := ""
	if t := Sanitize(slots.ThemeReport); t != "" {
		theme = "\nTHEME: " + t
	}
	return contextClause + "\n" + preserve + theme + "\nMODIFY: " + b.String() + "\n" + avoidClause, nil
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
	// Prefer GenWidth/GenHeight (resolved provider dims) over TargetWidth/
	// TargetHeight so the model sees the actual generation size, not the small
	// platform target (prevents "I'm generating a tiny 200px image" reasoning).
	dimW, dimH := slots.GenWidth, slots.GenHeight
	if dimW <= 0 || dimH <= 0 {
		dimW, dimH = slots.TargetWidth, slots.TargetHeight
	}
	if dimW > 0 && dimH > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", dimW, dimH))
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

// extremeRatioWidthThreshold is the long:short aspect ratio at/above which a
// target counts as an extreme banner/strip. gpt-image-2 clamps generation to
// 3:1 (resolveGptImage2Size), so a 4:1+ target can never be generated at its
// true ratio — the prompt must coach the model to compose for the wide format
// inside the clamped frame so the later outpaint has well-placed content to
// extend, not a centered subject with dead sides.
const extremeRatioWidthThreshold = 3.0

// extremeRatioHint returns a composition cue when the target is an extreme
// banner (≥3:1) or strip (≤1:3), nudging gpt-image-2 to keep the subject
// centered with clean, continuable background toward the long edges. Empty for
// ordinary ratios. Mirrors the "降维打击" guidance: a master image whose
// composition survives the ratio clamp + outpaint without breaking.
func extremeRatioHint(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	ar := float64(w) / float64(h)
	switch {
	case ar >= extremeRatioWidthThreshold:
		return "This is an ultra-wide panoramic banner. Use a landscape banner composition: keep the main subject centered, and let the background and scene elements extend cleanly toward the far left and far right so the wide format reads as one continuous scene with no dead space."
	case ar <= 1.0/extremeRatioWidthThreshold:
		return "This is an ultra-tall vertical strip. Use a portrait column composition: keep the main subject centered, and let the background and scene elements extend cleanly toward the top and bottom so the tall format reads as one continuous scene with no dead space."
	default:
		return ""
	}
}

// buildOutpaintPrompt is the instruction for the outpaint convergence step. The
// outpainter (gemini-2.5-flash-image / nano banana) is a prompt-driven *editing*
// model, NOT a mask-based inpainter — handing it a transparent-padded canvas and
// asking it to "fill the holes" fails, because it treats the transparent region
// as part of the picture (and renders it as a black/empty band) rather than as a
// fill mask. So instead we hand it the UNPADDED product and ask it to extend the
// existing scene outward to the new ratio.
//
// The model's output is used directly (cover-cropped to the exact target) — the
// master is no longer composited back over the center, because the mechanical
// seam between locked-center and AI-filled margins looked worse than a single
// coherent render. With nothing protecting the center pixels anymore, this prompt
// must carry the full anti-drift load: it frames the job as a preserve-everything
// outpaint/uncrop (existing content is fixed ground truth, only the added margins
// are invented) rather than a "regenerate wider" that invites the model to redraw
// the subject. dst gives the target ratio; genW/genH the source so the prompt can
// name the extension direction.
func buildOutpaintPrompt(genW, genH, dstW, dstH int) string {
	var b strings.Builder
	// Frame the task as outpainting/uncrop, not regeneration: the supplied image is
	// fixed ground truth and only the newly added margins may carry invented
	// content. With the master no longer composited back over the center, this
	// preserve-everything framing is the primary lever against subject drift.
	b.WriteString("OUTPAINT / UNCROP TASK: extend the supplied image onto a larger canvas. ")
	b.WriteString("This is an outpainting operation, NOT a regeneration — treat the supplied image as fixed, locked ground truth. ")
	b.WriteString("PRESERVE EXACTLY, pixel for pixel: the entire existing image and everything in it — the main subject and characters (identity, face, outfit, pose, expression), any text, logos and watermarks already present, the composition and the color grading. Do NOT redraw, move, resize, recolor, restyle, crop or cover any existing content; it must stay visually identical and remain centered. ")
	// Name the extension axis so the model knows where to invent content.
	if dstW*genH > genW*dstH {
		// Target is proportionally wider → grow left/right.
		b.WriteString("EXTEND only outward to the LEFT and RIGHT: generate brand-new background, scenery and atmosphere in the added side margins, seamlessly continuing the existing scene to fill the wider frame. ")
	} else if dstW*genH < genW*dstH {
		// Target is proportionally taller → grow top/bottom.
		b.WriteString("EXTEND only outward to the TOP and BOTTOM: generate brand-new background, scenery and atmosphere in the added top and bottom margins, seamlessly continuing the existing scene to fill the taller frame. ")
	} else {
		b.WriteString("EXTEND the existing scene outward to fill the new frame, seamlessly continuing the scenery. ")
	}
	if hint := extremeRatioHint(dstW, dstH); hint != "" {
		b.WriteString(hint)
		b.WriteString(" ")
	}
	b.WriteString("Match the original art style, colors, texture, lighting and perspective exactly so there are no visible seams and the whole reads as one continuous image. ")
	b.WriteString("Do NOT add blank, black, white or solid-color borders, and do NOT letterbox. Do NOT introduce any new text, logos or watermarks that are not already in the image. Fill the entire new canvas with coherent scenery. Production-ready, polished result.")
	return b.String()
}
