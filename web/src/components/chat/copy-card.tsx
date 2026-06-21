import * as React from "react";
import { motion } from "framer-motion";
import { Copy, Check, Megaphone } from "lucide-react";
import { cn } from "@/lib/utils";

// CopyCard renders the structured marketing copy produced by generate_copy:
// a main title, slogans, selling points, and a platform ad copy, each group
// labelled and individually copy-able. Read-only presentation; no JSON.
export function CopyCard({
  title,
  slogans,
  sellingPoints,
  platformCopy,
}: {
  title?: string;
  slogans?: string[];
  sellingPoints?: string[];
  platformCopy?: string;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
      className="rounded-lg border border-line/70 bg-bg-elev/60 px-4 py-3.5"
    >
      <div className="flex items-center gap-2.5">
        <Megaphone className="size-4 text-accent" />
        <span className="text-[13px] font-medium tracking-tight text-fg">宣发文案</span>
      </div>

      <div className="mt-3 flex flex-col gap-4">
        {title && (
          <Section label="主标题">
            <CopyLine text={title} className="text-sm font-semibold tracking-tight text-fg" />
          </Section>
        )}
        {slogans && slogans.length > 0 && (
          <Section label="广告语">
            <div className="flex flex-col gap-1.5">
              {slogans.map((s, i) => (
                <CopyLine key={i} text={s} className="text-[13px] leading-relaxed text-fg" />
              ))}
            </div>
          </Section>
        )}
        {sellingPoints && sellingPoints.length > 0 && (
          <Section label="核心卖点">
            <ul className="flex flex-col gap-1.5">
              {sellingPoints.map((p, i) => (
                <li key={i} className="flex items-start gap-2">
                  <span className="mt-[7px] size-1 shrink-0 rounded-full bg-accent/70" />
                  <CopyLine text={p} className="text-[13px] leading-relaxed text-fg" />
                </li>
              ))}
            </ul>
          </Section>
        )}
        {platformCopy && (
          <Section label="平台投放文案">
            <CopyLine text={platformCopy} className="text-[13px] leading-relaxed text-fg-mute" />
          </Section>
        )}
      </div>
    </motion.div>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-fg-dim">{label}</div>
      {children}
    </div>
  );
}

// CopyLine shows one piece of copy with a hover copy-to-clipboard affordance.
function CopyLine({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = React.useState(false);
  const onCopy = React.useCallback(() => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    });
  }, [text]);

  return (
    <div className="group/line flex items-start gap-2">
      <span className={cn("flex-1", className)}>{text}</span>
      <button
        type="button"
        onClick={onCopy}
        aria-label="复制"
        className="mt-0.5 shrink-0 text-fg-dim opacity-0 transition-all duration-200 ease-out hover:text-accent group-hover/line:opacity-100"
      >
        {copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}
