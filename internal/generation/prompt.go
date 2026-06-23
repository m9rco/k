package generation

import (
	"fmt"
	"math"
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
	// EditExtractLayer cuts ONE subject out of the source image onto a fully
	// transparent background, producing a same-size transparent PNG "layer" for
	// free compositing. It REQUIRES a transparency-capable adapter (Gemini); the
	// service refuses the intent when none is configured rather than producing a
	// fake/opaque background (design D1). RegionDesc, when present, names the
	// subject to extract; empty means the main foreground subject.
	EditExtractLayer EditKind = "extract_layer"
	// EditBackgroundFill removes the named foreground subjects and reconstructs a
	// clean, complete background at the SAME dimensions — the inpainted base layer
	// of a layer split. Output is opaque (no transparency needed), so it degrades
	// to the default adapter when no Gemini is configured. BackgroundDesc carries
	// the comma-joined subject descriptions to remove.
	EditBackgroundFill EditKind = "fill_background"
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
	// RegionDesc, when set, carries a structured description of ONE user-selected
	// region (produced by the vision region-description stage). It scopes the edit
	// to that subject: the MODIFY segment names the region subject and constrains
	// the model to leave everything outside it unchanged. Sanitized before
	// templating like every other slot; empty leaves behavior identical to before.
	RegionDesc string
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
	// SourceWidth/SourceHeight are the anchor source image's dimensions, used to
	// gauge how far the target ratio diverges from the source so the prompt can
	// add a copy-preserving recompose cue for medium ratio gaps (Q2). 0 disables.
	SourceWidth  int
	SourceHeight int
	SizeNote     string // e.g. 无文案 / 仅 logo / 圆角 / 透明底 / 安全区
	AdaptDesc    string // optional user description for the adaptation
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
	// QualityHints, when set, carries the quality-gate judge's improvement note
	// from a failed first-pass review. A REVISE segment is injected after THEME so
	// the regeneration (fed back to the same image model) addresses the flagged
	// compliance/subject/appeal issues. Sanitized before templating.
	QualityHints string
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
// it is injected. Logo/copy notes are expanded from Chinese to unambiguous English
// so the image model cannot misread "无文案" (no copy) as "no logo". Transparency
// notes are rewritten when the provider lacks that capability (design D4). Other
// notes (圆角/安全区/etc.) pass through unchanged. Most-specific patterns first.
func rewriteSizeNote(note string, supportsTransparency bool) string {
	if note == "" {
		return ""
	}
	if !supportsTransparency && (strings.Contains(note, "透明底") || strings.Contains(note, "透明背景")) {
		return transparencyRewrite
	}
	switch {
	case strings.Contains(note, "仅 logo") && strings.Contains(note, "无文案"):
		return "show the game LOGO only — no marketing copy, taglines or text overlays"
	case strings.Contains(note, "不带文案") && strings.Contains(note, "logo"):
		return "include the game LOGO; no marketing copy or text overlays"
	case strings.Contains(note, "带文案") && strings.Contains(note, "logo"):
		return "include both marketing copy/game title and the game LOGO"
	case strings.Contains(note, "无文案"):
		return "no marketing copy (no taglines, slogans or text overlays); keep the game LOGO fully visible and legible"
	case strings.Contains(note, "LOGO") && strings.Contains(note, "无广告语"):
		return "center or right-align the game LOGO; no advertising slogans"
	case strings.Contains(note, "不带游戏 logo"):
		return "no game LOGO; do not add or invent a logo"
	case strings.Contains(note, "无 logo") && !strings.Contains(note, "logo 位"):
		return "no game LOGO; do not add or invent a logo"
	case strings.Contains(note, "含清晰游戏 logo") || strings.Contains(note, "带游戏 logo"):
		return "include a clear, legible game LOGO"
	case strings.Contains(note, "须带文案"):
		return "prominently include marketing copy and the game title"
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
		if hint := extremeRatioHint(slots.GenWidth, slots.GenHeight, slots.TargetWidth, slots.TargetHeight); hint != "" {
			b.WriteString(hint)
			b.WriteString(" ")
		} else if hint := reproportionHint(slots.SourceWidth, slots.SourceHeight, slots.TargetWidth, slots.TargetHeight, slots.SizeNote); hint != "" {
			// Medium ratio gap (e.g. 16:9 source → 3:2 target): not extreme enough for
			// the safe-zone crop cue above, but far enough that free repaint drops the
			// copy. Inject a copy-preserving recompose constraint instead.
			b.WriteString(hint)
			b.WriteString(" ")
		}
		b.WriteString("Re-frame and extend/repaint the scene and background to fill the new aspect ratio naturally, rather than cropping; reposition the subject for a balanced composition at the target proportions. ")
		b.WriteString("The input may arrive at the target proportions with empty/transparent margins around the original artwork — treat those margins as scene to invent: seamlessly extend the background, scenery and atmosphere into them. Do NOT leave blank, black, white, solid-color or letterbox bands, and do NOT stretch or distort the subject to fill the frame. ")
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
	case EditExtractLayer:
		// Cut the named subject (or main foreground subject) out and place it on a
		// SOLID FLAT CHROMA-KEY background (pure green), NOT a "transparent" one.
		// Asking an image model for a "transparent PNG" makes it PAINT a literal
		// checkerboard pattern (transparency as seen in editors) into the image —
		// that看起来像马赛克 and carries no real alpha. A solid keyable color is
		// reliably honored; the server then keys it out to real alpha deterministically
		// (see chromaKeyToAlpha). Output keeps the source dimensions and the subject's
		// original placement so the layer drops back onto a source-sized canvas.
		if rd := Sanitize(slots.RegionDesc); rd != "" {
			b.WriteString("Isolate ONLY this subject from the image — ")
			b.WriteString(rd)
			b.WriteString(" — and remove everything else.")
		} else {
			b.WriteString("Isolate ONLY the main foreground subject from the image and remove everything else.")
		}
		b.WriteString(" Keep the SAME image dimensions and the subject at its original position and size, pixel-faithful (identity, colors, shading and crisp edges).")
		b.WriteString(" Replace the ENTIRE background and every area outside the subject with a SINGLE SOLID FLAT pure-green fill, exact color #00FF00 (RGB 0,255,0), uniform everywhere with no gradient, texture, pattern, shadow or checkerboard.")
		b.WriteString(" The subject itself MUST NOT contain any pure #00FF00 green. Do NOT draw a transparency/checkerboard pattern; do NOT add new content, outlines, glow or halos; do NOT alter the subject.")
	case EditBackgroundFill:
		// Inpaint base layer for a layer split: remove the foreground subjects and
		// reconstruct the background so the scene reads as complete with nothing on
		// it. Same dimensions, opaque output.
		b.WriteString("Remove the foreground subject(s) from the image")
		if rd := Sanitize(slots.BackgroundDesc); rd != "" {
			b.WriteString(" — specifically: ")
			b.WriteString(rd)
			b.WriteString(" —")
		}
		b.WriteString(" and reconstruct a clean, complete background where they were, seamlessly continuing the surrounding scenery, lighting and perspective so the result looks like a natural empty background.")
		b.WriteString(" KEEP the brand LOGO and all original scenery/background intact — only the listed foreground subjects are removed.")
		b.WriteString(" Keep the SAME image dimensions and all genuine background content unchanged. Do NOT leave holes, silhouettes, blur smears or ghosting; do NOT invent new subjects, characters, text or logos.")
	default:
		return "", fmt.Errorf("unsupported edit kind %q", slots.Kind)
	}

	if slots.ReuseComposition {
		b.WriteString(" Reuse the reference image's composition and base elements; adapt the background to suit the new character.")
	}

	// Region scoping: when the user selected one region, name its subject and
	// constrain the edit to it, leaving the rest of the frame untouched. Pure
	// prompt-layer (no provider mask). Appended to the MODIFY body; a matching
	// preservation cue is added to AVOID below. Skipped for EditExtractLayer, which
	// uses RegionDesc to name what to EXTRACT (handled in its case above), not to
	// scope an in-place edit.
	regionScoped := false
	if rd := Sanitize(slots.RegionDesc); rd != "" && slots.Kind != EditExtractLayer {
		regionScoped = true
		b.WriteString(" Apply this change ONLY to the selected region subject — ")
		b.WriteString(rd)
		b.WriteString(" — and keep every other region, the overall composition and all other subjects unchanged.")
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
	revise := ""
	if h := Sanitize(slots.QualityHints); h != "" {
		revise = "\nREVISE: A prior attempt was rejected by quality review. Fix these issues this time: " + h
	}
	avoid := avoidClause
	if regionScoped {
		avoid = avoidClause + " Do NOT modify pixels outside the selected region or alter any other subject in the frame."
	}
	return contextClause + "\n" + preserve + theme + revise + "\nMODIFY: " + b.String() + "\n" + avoid, nil
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
// target counts as an extreme banner/strip. It is sourced from gptImage2MaxRatio
// (the 3:1 generation clamp) so this prompt-side threshold and the converge-side
// extremeConvergeRatio are the same constant: a target at/above it can never be
// generated at its true ratio, so the prompt must coach the model to compose for
// the final narrow canvas inside the clamped frame — placing subject/logo/copy in
// a central safe band so the later deterministic cover crops only background, not
// the subject.
const extremeRatioWidthThreshold = gptImage2MaxRatio

// safeBandFraction returns the central fraction (0,1] of the generated canvas that
// survives a cover crop from the generation ratio to the target ratio — i.e. the
// safe band the subject must stay inside. keepFrac ≈ genRatio / dstRatio, where
// both ratios are long:short (so the result is symmetric for banners and strips):
// a 3:1 gen cover-cropped to a 6:1 target keeps the central 50% of the long-axis-
// perpendicular dimension; to a 4:1 target, ~75%. Rounded to a 5% grid (models
// reason better about coarse fractions than precise percentages) and clamped to
// [0.25, 0.95] so the cue stays sane even at pathological ratios. Returns 0 for
// any non-positive dimension.
func safeBandFraction(genW, genH, dstW, dstH int) float64 {
	if genW <= 0 || genH <= 0 || dstW <= 0 || dstH <= 0 {
		return 0
	}
	genRatio := math.Max(float64(genW)/float64(genH), float64(genH)/float64(genW))
	dstRatio := math.Max(float64(dstW)/float64(dstH), float64(dstH)/float64(dstW))
	if dstRatio <= genRatio {
		return 1 // target no more extreme than gen — nothing is cropped away
	}
	frac := genRatio / dstRatio
	// Round to the nearest 5% grid.
	frac = math.Round(frac*20) / 20
	if frac < 0.25 {
		frac = 0.25
	}
	if frac > 0.95 {
		frac = 0.95
	}
	return frac
}

// extremeRatioHint returns a safe-zone composition cue when the target is an
// extreme banner (≥3:1) or strip (≤1:3). Because gpt-image-2 clamps generation to
// 3:1, an extreme target is generated narrower than its final shape and converged
// by a deterministic cover crop — so the model must keep the subject, logo and
// copy inside the central band that survives that crop (computed by
// safeBandFraction), leaving only croppable background toward the cropped edges.
// genW/genH are the resolved generation dims (0 falls back to assuming the 3:1
// clamp). Empty for ordinary ratios. See design D2.
func extremeRatioHint(genW, genH, dstW, dstH int) string {
	if dstW <= 0 || dstH <= 0 {
		return ""
	}
	ar := float64(dstW) / float64(dstH)
	// Fall back to the 3:1 clamp when gen dims are unknown, so the percentage is
	// still computable (and matches what the resolver would produce).
	gw, gh := genW, genH
	if gw <= 0 || gh <= 0 {
		if ar >= 1 {
			gw, gh = 3, 1
		} else {
			gw, gh = 1, 3
		}
	}
	switch {
	case ar >= extremeRatioWidthThreshold:
		frac := safeBandFraction(gw, gh, dstW, dstH)
		pct := int(math.Round(frac * 100))
		edge := int(math.Round((1 - frac) / 2 * 100))
		return fmt.Sprintf("CRITICAL FRAMING CONSTRAINT — this image WILL be mechanically center-cropped to an ultra-wide banner: the top %d%% AND bottom %d%% of the height (%d%% total) are DELETED, only the central %d%% horizontal band (中央 %d%% 高度带) survives. This crop is automatic and unavoidable. Therefore: (1) place the ENTIRE main subject, ALL characters' heads and faces, the LOGO and all marketing copy WHOLLY INSIDE that central band — scale them DOWN if needed so they fit with margin to spare; nothing essential may touch or cross the band's edges. (2) Fill the top %d%% and bottom %d%% ONLY with expendable, croppable background, sky, ground or atmosphere — absolutely NO faces, text, logos or subject parts there. Treat the top/bottom strips as throw-away padding. A subject that fills the full frame height WILL be decapitated by the crop.", edge, edge, edge*2, pct, pct, edge, edge)
	case ar <= 1.0/extremeRatioWidthThreshold:
		frac := safeBandFraction(gw, gh, dstW, dstH)
		pct := int(math.Round(frac * 100))
		edge := int(math.Round((1 - frac) / 2 * 100))
		return fmt.Sprintf("CRITICAL FRAMING CONSTRAINT — this image WILL be mechanically center-cropped to an ultra-tall strip: the left %d%% AND right %d%% of the width (%d%% total) are DELETED, only the central %d%% vertical band (中央 %d%% 宽度带) survives. This crop is automatic and unavoidable. Therefore: (1) place the ENTIRE main subject, ALL characters' faces, the LOGO and all marketing copy WHOLLY INSIDE that central band — scale them DOWN if needed so they fit with margin to spare; nothing essential may touch or cross the band's edges. (2) Fill the left %d%% and right %d%% ONLY with expendable, croppable background or atmosphere — absolutely NO faces, text, logos or subject parts there. Treat the left/right strips as throw-away padding. A subject that fills the full frame width WILL be sliced by the crop.", edge, edge, edge*2, pct, pct, edge, edge)
	default:
		return ""
	}
}

// reproportionHint returns a copy-preserving recompose cue when the source and
// target aspect ratios diverge beyond the crop fast-path tolerance but the
// target is NOT extreme (extremeRatioHint handles ≥3:1). The caller's else-if
// already guarantees extremeRatioHint returned ""; this function only needs to
// check the lower bound (ratio gap > ratioTolerance). Empty when source dims
// are unknown or the ratio gap is within tolerance.
func reproportionHint(srcW, srcH, dstW, dstH int, sizeNote string) string {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return ""
	}
	arSrc := float64(srcW) / float64(srcH)
	arDst := float64(dstW) / float64(dstH)
	diff := math.Abs(arSrc-arDst) / arDst
	if diff <= ratioTolerance {
		return "" // ratio gap is small; no recompose constraint needed
	}
	// Medium ratio gap: the model must genuinely recompose the layout.
	// Always require subject + LOGO. Add copy requirement unless 无文案.
	noCopy := strings.Contains(sizeNote, "无文案")
	if noCopy {
		return "RECOMPOSE CONSTRAINT — the target aspect ratio differs significantly from the source. When recomposing, you MUST keep the main subject and LOGO fully visible and legible at the new proportions; reposition them as needed — do NOT crop them out or omit them."
	}
	return "RECOMPOSE CONSTRAINT — the target aspect ratio differs significantly from the source. When recomposing, you MUST keep the main subject, LOGO, and all marketing copy (title, tagline, text labels) fully visible and legible at the new proportions; reposition them as needed — do NOT drop, crop, or omit any of them."
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
	if hint := extremeRatioHint(genW, genH, dstW, dstH); hint != "" {
		b.WriteString(hint)
		b.WriteString(" ")
	}
	b.WriteString("Match the original art style, colors, texture, lighting and perspective exactly so there are no visible seams and the whole reads as one continuous image. ")
	b.WriteString("Do NOT add blank, black, white or solid-color borders, and do NOT letterbox. Do NOT introduce any new text, logos or watermarks that are not already in the image. Fill the entire new canvas with coherent scenery. Production-ready, polished result.")
	return b.String()
}
