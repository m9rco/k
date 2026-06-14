import * as React from "react";

// Vendor brand marks, keyed by the catalog's iconKey. Anthropic / Google /
// Alibaba use their brand logos (rendered in brand colors for recognizability);
// other vendors use a tasteful monochrome monogram. Used only to indicate a
// model's origin inside this internal tool. Unknown keys fall back to a neutral
// dot so the UI never breaks.

type IconProps = { className?: string };

// --- OpenAI: monochrome knot mark (currentColor, fits the design tokens) ---
function OpenAI({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M22.28 9.82a5.98 5.98 0 0 0-.52-4.91 6.05 6.05 0 0 0-6.51-2.9A6 6 0 0 0 4.98 4.18a5.98 5.98 0 0 0-4 2.9 6.05 6.05 0 0 0 .74 7.1 5.98 5.98 0 0 0 .51 4.91 6.05 6.05 0 0 0 6.52 2.9A5.98 5.98 0 0 0 13.26 24a6.05 6.05 0 0 0 5.77-4.19 5.98 5.98 0 0 0 4-2.9 6.05 6.05 0 0 0-.75-7.1zM13.26 22.43a4.48 4.48 0 0 1-2.88-1.04l4.95-2.86a.8.8 0 0 0 .4-.7v-6.98l2.1 1.21a.07.07 0 0 1 .04.06v5.78a4.5 4.5 0 0 1-4.61 4.53zM4.6 18.3a4.48 4.48 0 0 1-.54-3.02l4.95 2.86a.8.8 0 0 0 .81 0l6.05-3.49v2.42a.07.07 0 0 1-.03.06l-5.02 2.9a4.5 4.5 0 0 1-6.22-1.73zM3.3 7.86A4.48 4.48 0 0 1 5.64 5.9v5.89a.8.8 0 0 0 .4.7l6.05 3.49-2.1 1.21a.07.07 0 0 1-.07 0l-5.02-2.9A4.5 4.5 0 0 1 3.3 7.86zm17.18 4l-6.05-3.5 2.1-1.2a.07.07 0 0 1 .07 0l5.02 2.9a4.5 4.5 0 0 1-.68 8.12v-5.89a.8.8 0 0 0-.46-.43zM22.6 8.9l-4.95-2.86a.8.8 0 0 0-.81 0l-6.05 3.49V7.1a.07.07 0 0 1 .03-.06l5.02-2.9a4.5 4.5 0 0 1 6.76 4.76zM9.6 13.1l-2.1-1.21a.07.07 0 0 1-.04-.06V6.05a4.5 4.5 0 0 1 7.5-3.36l-4.95 2.86a.8.8 0 0 0-.4.7z" />
    </svg>
  );
}

// --- Anthropic: the radial "spark/burst" mark in Anthropic clay ---
function Anthropic({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden>
      <g fill="#D97757" transform="translate(12 12)">
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" />
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" transform="rotate(45)" />
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" transform="rotate(90)" />
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" transform="rotate(135)" />
      </g>
    </svg>
  );
}

// --- Google: official multi-color "G" logo ---
function Google({ className }: IconProps) {
  return (
    <svg viewBox="0 0 48 48" className={className} aria-hidden>
      <path fill="#FFC107" d="M43.611 20.083H42V20H24v8h11.303c-1.649 4.657-6.08 8-11.303 8-6.627 0-12-5.373-12-12s5.373-12 12-12c3.059 0 5.842 1.154 7.961 3.039l5.657-5.657C34.046 6.053 29.268 4 24 4 12.955 4 4 12.955 4 24s8.955 20 20 20 20-8.955 20-20c0-1.341-.138-2.65-.389-3.917Z" />
      <path fill="#FF3D00" d="m6.306 14.691 6.571 4.819C14.655 15.108 18.961 12 24 12c3.059 0 5.842 1.154 7.961 3.039l5.657-5.657C34.046 6.053 29.268 4 24 4 16.318 4 9.656 8.337 6.306 14.691Z" />
      <path fill="#4CAF50" d="M24 44c5.166 0 9.86-1.977 13.409-5.192l-6.19-5.238C29.211 35.091 26.715 36 24 36c-5.202 0-9.619-3.317-11.283-7.946l-6.522 5.025C9.505 39.556 16.227 44 24 44Z" />
      <path fill="#1976D2" d="M43.611 20.083H42V20H24v8h11.303a12.04 12.04 0 0 1-4.087 5.571l6.19 5.238C36.971 39.205 44 34 44 24c0-1.341-.138-2.65-.389-3.917Z" />
    </svg>
  );
}

// --- Alibaba: the brand "smile" ribbon in Alibaba orange ---
function Alibaba({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden fill="none" stroke="#FF6A00" strokeLinecap="round" strokeLinejoin="round">
      {/* smiling mouth */}
      <path d="M5 12c1.8 3.2 4.2 4.8 7 4.8s5.2-1.6 7-4.8" strokeWidth="2.4" />
      {/* two eyes */}
      <path d="M8.2 8.2v1.4M15.8 8.2v1.4" strokeWidth="2.4" />
    </svg>
  );
}

function Letter({ ch, className }: { ch: string; className?: string }) {
  return (
    <span
      className={className}
      style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", fontWeight: 700, fontSize: "0.7em" }}
      aria-hidden
    >
      {ch}
    </span>
  );
}

const VENDOR_ICONS: Record<string, React.ComponentType<IconProps>> = {
  openai: OpenAI,
  anthropic: Anthropic,
  google: Google,
  alibaba: Alibaba,
  deepseek: (p) => <Letter ch="DS" {...p} />,
  doubao: (p) => <Letter ch="豆" {...p} />,
};

export function VendorIcon({ iconKey, className }: { iconKey: string; className?: string }) {
  const Cmp = VENDOR_ICONS[iconKey];
  if (Cmp) return <Cmp className={className} />;
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <circle cx="12" cy="12" r="4" />
    </svg>
  );
}
