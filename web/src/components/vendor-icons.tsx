import * as React from "react";

// Vendor brand marks, keyed by the catalog's iconKey. These are simplified,
// monochrome (currentColor) glyphs evoking each vendor — internal tool use only.
// Unknown keys fall back to a neutral dot so the UI never breaks.

type IconProps = { className?: string };

function OpenAI({ className }: IconProps) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M22.28 9.82a5.98 5.98 0 0 0-.52-4.91 6.05 6.05 0 0 0-6.51-2.9A6 6 0 0 0 4.98 4.18a5.98 5.98 0 0 0-4 2.9 6.05 6.05 0 0 0 .74 7.1 5.98 5.98 0 0 0 .51 4.91 6.05 6.05 0 0 0 6.52 2.9A5.98 5.98 0 0 0 13.26 24a6.05 6.05 0 0 0 5.77-4.19 5.98 5.98 0 0 0 4-2.9 6.05 6.05 0 0 0-.75-7.1zM13.26 22.43a4.48 4.48 0 0 1-2.88-1.04l4.95-2.86a.8.8 0 0 0 .4-.7v-6.98l2.1 1.21a.07.07 0 0 1 .04.06v5.78a4.5 4.5 0 0 1-4.61 4.53zM4.6 18.3a4.48 4.48 0 0 1-.54-3.02l4.95 2.86a.8.8 0 0 0 .81 0l6.05-3.49v2.42a.07.07 0 0 1-.03.06l-5.02 2.9a4.5 4.5 0 0 1-6.22-1.73zM3.3 7.86A4.48 4.48 0 0 1 5.64 5.9v5.89a.8.8 0 0 0 .4.7l6.05 3.49-2.1 1.21a.07.07 0 0 1-.07 0l-5.02-2.9A4.5 4.5 0 0 1 3.3 7.86zm17.18 4l-6.05-3.5 2.1-1.2a.07.07 0 0 1 .07 0l5.02 2.9a4.5 4.5 0 0 1-.68 8.12v-5.89a.8.8 0 0 0-.46-.43zM22.6 8.9l-4.95-2.86a.8.8 0 0 0-.81 0l-6.05 3.49V7.1a.07.07 0 0 1 .03-.06l5.02-2.9a4.5 4.5 0 0 1 6.76 4.76zM9.6 13.1l-2.1-1.21a.07.07 0 0 1-.04-.06V6.05a4.5 4.5 0 0 1 7.5-3.36l-4.95 2.86a.8.8 0 0 0-.4.7zM10.75 10.6L12 9.6l1.25.72v1.44L12 12.49l-1.25-.72z" />
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

// Brand marks that are awkward to trace are rendered as a tasteful monogram.
const VENDOR_ICONS: Record<string, React.ComponentType<IconProps>> = {
  openai: OpenAI,
  gemini: ({ className }) => (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M12 2c.6 5.4 4.6 9.4 10 10-5.4.6-9.4 4.6-10 10-.6-5.4-4.6-9.4-10-10 5.4-.6 9.4-4.6 10-10z" />
    </svg>
  ),
  claude: ({ className }) => (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M5 19l5.2-14h2.2L17.6 19h-2.3l-1.5-4.3H8.8L7.3 19H5zm4.5-6.2h4L11.5 7l-2 5.8z" />
    </svg>
  ),
  deepseek: (p) => <Letter ch="DS" {...p} />,
  doubao: (p) => <Letter ch="豆" {...p} />,
  qwen: (p) => <Letter ch="Q" {...p} />,
  wan: (p) => <Letter ch="W" {...p} />,
  veo: (p) => <Letter ch="V" {...p} />,
  happyhorse: (p) => <Letter ch="马" {...p} />,
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
