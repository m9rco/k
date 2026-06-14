import * as React from "react";

// Vendor marks, keyed by the catalog's iconKey. All marks are monochrome and
// inherit the surrounding text color (currentColor), so they share the card's
// selected/unselected tone and stay visually unified across vendors. Used only
// to indicate a model's origin inside this internal tool. Unknown keys fall back
// to a neutral dot so the UI never breaks.

type IconProps = { className?: string };

// --- OpenAI: knot mark ---
function OpenAI({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M22.28 9.82a5.98 5.98 0 0 0-.52-4.91 6.05 6.05 0 0 0-6.51-2.9A6 6 0 0 0 4.98 4.18a5.98 5.98 0 0 0-4 2.9 6.05 6.05 0 0 0 .74 7.1 5.98 5.98 0 0 0 .51 4.91 6.05 6.05 0 0 0 6.52 2.9A5.98 5.98 0 0 0 13.26 24a6.05 6.05 0 0 0 5.77-4.19 5.98 5.98 0 0 0 4-2.9 6.05 6.05 0 0 0-.75-7.1zM13.26 22.43a4.48 4.48 0 0 1-2.88-1.04l4.95-2.86a.8.8 0 0 0 .4-.7v-6.98l2.1 1.21a.07.07 0 0 1 .04.06v5.78a4.5 4.5 0 0 1-4.61 4.53zM4.6 18.3a4.48 4.48 0 0 1-.54-3.02l4.95 2.86a.8.8 0 0 0 .81 0l6.05-3.49v2.42a.07.07 0 0 1-.03.06l-5.02 2.9a4.5 4.5 0 0 1-6.22-1.73zM3.3 7.86A4.48 4.48 0 0 1 5.64 5.9v5.89a.8.8 0 0 0 .4.7l6.05 3.49-2.1 1.21a.07.07 0 0 1-.07 0l-5.02-2.9A4.5 4.5 0 0 1 3.3 7.86zm17.18 4l-6.05-3.5 2.1-1.2a.07.07 0 0 1 .07 0l5.02 2.9a4.5 4.5 0 0 1-.68 8.12v-5.89a.8.8 0 0 0-.46-.43zM22.6 8.9l-4.95-2.86a.8.8 0 0 0-.81 0l-6.05 3.49V7.1a.07.07 0 0 1 .03-.06l5.02-2.9a4.5 4.5 0 0 1 6.76 4.76zM9.6 13.1l-2.1-1.21a.07.07 0 0 1-.04-.06V6.05a4.5 4.5 0 0 1 7.5-3.36l-4.95 2.86a.8.8 0 0 0-.4.7z" />
    </svg>
  );
}

// --- Anthropic: radial "spark/burst" mark ---
function Anthropic({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden>
      <g fill="currentColor" transform="translate(12 12)">
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" />
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" transform="rotate(45)" />
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" transform="rotate(90)" />
        <rect x="-1.15" y="-10" width="2.3" height="20" rx="1.15" transform="rotate(135)" />
      </g>
    </svg>
  );
}

// --- Google: monochrome "G" mark ---
function Google({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M12 2a10 10 0 1 0 6.6 17.5c1.9-1.8 2.9-4.4 2.9-7.5 0-.7-.06-1.3-.18-1.9H12v3.7h5.4c-.24 1.3-.96 2.4-2.04 3.15v2.6h3.3A9.96 9.96 0 0 0 12 22 10 10 0 0 1 12 2zm0 3.7c1.5 0 2.85.52 3.9 1.53l2.6-2.6A9.6 9.6 0 0 0 12 2 10 10 0 0 0 3 7.5l3.05 2.37A5.96 5.96 0 0 1 12 5.7z" />
    </svg>
  );
}

// --- Alibaba: "smile" ribbon ---
function Alibaba({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
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
