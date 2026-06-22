# Fix platform-adapt aspect distortion + direct backfill for matching sizes

## Why

Real adapt run (`data/logs/app.log`, session `sess_e8af652c9c3e5d40`, adapting two
references to `taptap.banner.search-2080x828`) surfaced two美感-killing defects and
two efficiency问题:

1. **人物被压扁（横向形变）.** The 1920×1080 (16:9) anchor was pre-upscaled to the
   generation canvas 2752×1088 (≈2.5:1) via `crop.ModeScale` — a **plain stretch
   that ignores aspect ratio** (`internal/generation/service.go:755`). The subject is
   horizontally stretched ~1.43× *before* the model sees it, so gpt-image-2 faithfully
   reproduces distorted characters. The downstream converge step is innocent
   (`gen.converge mode=scale`, gen 2.529 ≈ target 2.512 — no further distortion).

2. **同尺寸参考仍被重新生成.** This run had 2 references → `multiRef=true` →
   `internal/generation/adapt.go:114` forces **every** target size to AI repaint.
   For sizes that are identical to the anchor — e.g. `taptap.banner.welfare-1920x1080`
   and `taptap.banner.topic-home` (both 1920×1080) — a 166s generation runs where a
   direct backfill of the reference would be pixel-perfect, instant, and distortion-free.

3. **生成耗时 166s/张** (`gen.provider_ok duration_ms=166055`). The size mapping
   upscales even already-large targets to the ~3MP quality budget (1.72MP target →
   2.99MP gen), inflating latency for negligible sharpness gain on large placements.

4. **极端展宽缺乏补全约束.** Once the pre-upscale stops stretching, placing a 16:9
   source on a 2.5:1 canvas introduces side margins; without an explicit instruction to
   extend the scene, the result risks flat/empty bands — also a美感 defect.

宣发素材的第一标准是美感与主体保真，形变与多余等待都不可接受。

## What Changes

- **Aspect-preserving pre-upscale (Issue 1).** The adapt pre-upscale MUST preserve the
  source aspect ratio — never stretch. The source is fit (contain/letterbox) onto the
  generation canvas at its native proportions, or uniformly upscaled; `ModeScale`
  (non-uniform stretch) is forbidden for this step.
- **Anchor-ratio direct backfill, including multi-reference (Issue 2).** Even with a
  reference group (≥2), each target size whose aspect ratio matches the **anchor** within
  tolerance (and shares orientation) takes the deterministic fast path: exact-dimension
  match → verbatim backfill of the anchor; same-ratio → clean equal-ratio rescale/crop.
  Only genuine reshapes still go AI repaint (feeding the whole group). Sizes that take
  the fast path are produced from the anchor only.
- **Right-size the generation budget to cut latency (Issue 3).** When the target is
  already at/above a high-resolution threshold, the generation pixel budget MUST NOT
  gratuitously upscale beyond the target's own resolution. Small-target sharpening
  (upscaling sub-budget targets for a crisper downsample) is preserved.
- **Coherent scene extension into introduced margins (Issue 4).** When the
  aspect-preserving pre-upscale introduces side/top margins, the model MUST extend the
  scene into them for a coherent wide composition; the final product MUST NOT show flat
  pad bands.

## Impact

- Affected specs: `platform-adaptation` (1 ADDED requirement, 3 MODIFIED requirements).
- Affected code (apply stage): `internal/generation/service.go` (pre-upscale mode +
  budget), `internal/generation/adapt.go` (anchor-ratio routing in multi-ref, backfill),
  `internal/generation/http_provider.go` (`resolveGptImage2Size` budget cap), adapt
  prompt skeleton (margin-extension instruction).
- No frontend changes required: backfilled sizes already render as crop-origin products;
  the adapt pipeline UI handles per-size crop-vs-AI routing today.
- No API/schema changes.
