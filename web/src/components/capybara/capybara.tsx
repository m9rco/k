import { motion } from "framer-motion";
import "./capybara.css";

// Pixel-art capybara mascot. Built from a character bitmap rendered as <rect>s
// (no image asset). A few overlay layers (eyelids, a scratching paw, a yawn
// mouth, a grass blade) carry CSS keyframe animations for idle charm. All
// motion pauses under prefers-reduced-motion (see capybara.css).

const PX = 5; // size of one pixel cell, in svg user units
const COLS = 14;
const ROWS = 14;

// Palette. Space = transparent.
const PAL: Record<string, string> = {
  d: "#5c4516", // dark outline / shadow
  m: "#9c7b3f", // main fur
  l: "#b89554", // light cheek
  h: "#e8d5a3", // belly highlight
  n: "#4a3518", // snout shade
  e: "#1a1a1a", // eye
};

// 14×14 sitting capybara blob: ears, blocky head+body, eyes, snout, belly, feet.
const BITMAP = [
  "..dd......dd..",
  ".dmmd....dmmd.",
  ".dmmddddddmmd.",
  ".dmmmmmmmmmmd.",
  "dmmmmmmmmmmmmd",
  "dmllmmmmmmllmd",
  "dmllmmmmmmllmd",
  "dmmmeemmeemmmd",
  "dmmmmmmmmmmmmd",
  "dmmmmnnnnmmmmd",
  "dmmmmnnnnmmmmd",
  ".dmmmmmmmmmmd.",
  ".dhhhhhhhhhhd.",
  "..dd.dd.dd.d..",
];

function bitmapRects() {
  const rects: { x: number; y: number; fill: string; key: string }[] = [];
  for (let r = 0; r < BITMAP.length; r++) {
    const row = BITMAP[r];
    for (let c = 0; c < row.length; c++) {
      const ch = row[c];
      const fill = PAL[ch];
      if (!fill) continue;
      rects.push({ x: c * PX, y: r * PX, fill, key: `${r}-${c}` });
    }
  }
  return rects;
}

const RECTS = bitmapRects();

export function Capybara({ className }: { className?: string }) {
  return (
    <motion.svg
      className={className}
      width={COLS * PX}
      height={ROWS * PX}
      viewBox={`0 0 ${COLS * PX} ${ROWS * PX}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label="卡皮巴拉吉祥物"
      // Gentle breathing idle on the whole body.
      style={{ transformOrigin: "50% 100%" }}
    >
      <g className="capy-breathe">
        {RECTS.map((r) => (
          <rect key={r.key} x={r.x} y={r.y} width={PX} height={PX} fill={r.fill} />
        ))}

        {/* Eyelids: cover the eye pixels (row 7, cols 4-5 and 8-9), blink via
            opacity keyframes. */}
        <g className="capy-blink">
          <rect x={4 * PX} y={7 * PX} width={2 * PX} height={PX} fill={PAL.m} />
          <rect x={8 * PX} y={7 * PX} width={2 * PX} height={PX} fill={PAL.m} />
        </g>

        {/* Yawn mouth: a dark rect over the snout that grows occasionally. */}
        <rect className="capy-yawn" x={6 * PX} y={10 * PX} width={2 * PX} height={PX} fill={PAL.e} />

        {/* Scratching paw: a small light rect near the cheek that lifts/taps. */}
        <rect className="capy-paw" x={1 * PX} y={9 * PX} width={2 * PX} height={2 * PX} fill={PAL.l} />

        {/* Grass blade in mouth, wiggles. */}
        <g className="capy-grass">
          <rect x={13 * PX} y={9 * PX} width={PX} height={PX} fill="#6b8e23" />
          <rect x={13.6 * PX} y={8 * PX} width={PX * 0.6} height={PX} fill="#7ea33a" />
        </g>
      </g>
    </motion.svg>
  );
}
