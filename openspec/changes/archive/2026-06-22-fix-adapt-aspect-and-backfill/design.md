# Design — fix-adapt-aspect-and-backfill

## Context

Platform adaptation has two paths: a deterministic crop fast path (no AI) and an AI
repaint (gpt-image-2). Routing today (`adapt.go:111-156`):

- single reference → ratio-based split (`aspectClose` → crop; else AI);
- reference group (≥2) → **always** AI, to avoid silently dropping auxiliary refs.

The AI path pre-upscales the source to the generation canvas (`service.go:754-763`) then
converges the provider output to the exact catalog size (`service.go:857-898`,
`convergeMode` in `adapt.go:236`).

The observed run exposed that (a) the pre-upscale stretches non-uniformly, and (b) the
multi-ref always-AI rule regenerates sizes that equal the anchor.

## Decisions

### D1 — Pre-upscale must preserve aspect ratio

`service.go:755` calls `crop.CropBytesWithOptions(srcBytes, wantW, wantH, {Mode: ModeScale})`.
`ModeScale` resizes to the exact target W×H regardless of source ratio → non-uniform
stretch when `wantW/wantH ≠ srcW/srcH`. For a 16:9 source onto a 2.5:1 canvas this
widens the subject ~1.43×.

**Decision:** the pre-upscale uses an aspect-preserving fit (`contain` — uniform scale to
fit inside the canvas, transparent/neutral margins) instead of `ModeScale`. The model
then receives an undistorted subject plus margins to extend into (see D4). This is the
narrowest correct fix: the converge step already handles final exact sizing.

*Alternative considered — uniform `cover` (fill+crop) instead of `contain`:* rejected as
the default because cover crops the source before the model sees it, risking loss of
LOGO/copy at the edges (the very elements the prompt mandates preserving). `contain`
keeps everything and lets the model paint the margins.

### D2 — Anchor-ratio direct backfill, including multi-reference

Currently `multiRef` short-circuits to AI for *every* size. But the fast path only ever
operates on the anchor (`refs[0]`), and the auxiliary-refs concern only applies when the
composition actually changes. When a target's ratio already matches the anchor, there is
no reshape — the auxiliary refs would contribute nothing a repaint could use that the
anchor doesn't already carry.

**Decision:** evaluate `aspectClose(anchor, target)` **before** the multi-ref gate. If it
matches (ratio within `ratioTolerance` AND same orientation), take the deterministic fast
path from the anchor — even in multi-ref. Only ratio/orientation reshapes keep the
always-AI multi-ref behavior (still feeding the whole group).

Two sub-cases inside the fast path:

- **Exact dimensions** (`anchor.W==target.W && anchor.H==target.H`): backfill the anchor
  verbatim — a pixel copy (re-encoded to the catalog format / lossless policy). No crop
  math. This is the `1920×1080 → taptap.welfare-1920×1080 / topic-home` case.
- **Same ratio, different size**: equal-ratio rescale via the existing crop path
  (`CropToSizes` with `ModeCover`, which for an equal ratio is a clean scale with no
  crop loss).

The existing single-ref branch already does the same `aspectClose`→crop; this decision
generalizes the gate so the anchor check runs regardless of ref count. The "提供参考组时
强制 AI 重绘" requirement narrows: forced AI applies only to **reshape** sizes, not
ratio-matching sizes.

*Why not also fast-path when an auxiliary ref matches but the anchor doesn't?* The anchor
is the single content truth source (existing contract); producing a size from an
auxiliary image would change which image is authoritative for that product. Out of scope.

### D3 — Right-size generation budget to cut latency

`resolveGptImage2Size` (`http_provider.go:244`) always solves W,H from a fixed ~3MP
budget (`gptImage2GenBudget = 3_000_000`). For a 1.72MP target (2080×828) this upscales
to 2.99MP gen, then downsamples — 166s for marginal benefit on a large placement.

**Decision:** cap the generation budget at the target's own pixel count when the target
already meets a "large enough" threshold, so large targets generate at ≈their own size
rather than being inflated to 3MP. The existing small-target behavior (upscale sub-budget
targets toward the budget for a sharper downsample, per the "gpt-image-2 同比例放大出图
再下采样" requirement) is unchanged. Concretely: `effectiveBudget = min(gptImage2GenBudget,
max(targetPixels, smallTargetFloor))` — leaving the ratio-clamp and edge/grid rounding
untouched.

This keeps the "no silent caps" contract: the existing `gen.adapt_above_2k` log already
surfaces clamping; we add nothing that silently shrinks below the target.

*Risk:* generating closer to target size yields slightly less AI super-resolution detail.
Acceptable — at ≥~1.7MP the placement is large and the source is already high-res; the
166s→ lower latency win dominates for宣发 iteration速度.

### D4 — Coherent extension into pre-upscale margins

With D1, a 16:9 source on a 2.5:1 canvas has neutral side margins. The repaint prompt
already says "Re-frame and extend/repaint the scene and background to fill the new aspect
ratio naturally, rather than cropping" — but with a stretched input there were no margins,
so that clause was effectively dormant. With D1's letterboxed input it becomes load-
bearing.

**Decision:** strengthen the adapt prompt skeleton so margins introduced by the
aspect-preserving fit are explicitly framed as scene to extend (not to leave blank), and
let the existing pixel pre-filter (`gen.pixel_failed` on纯色留白带) catch flat-band
regressions. No new pipeline stage.

## Routing decision table (post-change)

| refs | anchor↔target ratio | dims equal | path |
|------|--------------------|-----------|------|
| 1    | match              | yes       | crop verbatim backfill |
| 1    | match              | no        | crop equal-ratio rescale |
| 1    | reshape            | —         | AI repaint (anchor) |
| ≥2   | match              | yes       | crop verbatim backfill (anchor) |
| ≥2   | match              | no        | crop equal-ratio rescale (anchor) |
| ≥2   | reshape            | —         | AI repaint (whole group) |

## Out of scope

- Choosing a different anchor / fast-pathing auxiliary refs.
- Changing the converge-mode auto-selection or extreme-ratio cover logic.
- Frontend changes (crop-origin products already render correctly).
