// BrandMark is a single-color line mark (no gradients, no color blocks) to keep
// the sober aesthetic. Inherits currentColor.
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <path d="M12 3 20 7.5 V16.5 L12 21 L4 16.5 V7.5 Z" />
      <circle cx="12" cy="12" r="2.4" />
    </svg>
  );
}
