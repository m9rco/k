import { motion } from "framer-motion";
import { X } from "lucide-react";
import type { CapsuleOption } from "@/store/types";

// FollowUpBubble shows proactive suggestions after a productive turn.
export function FollowUpBubble({
  message,
  options,
  dismissed,
  onSubmit,
  onDismiss,
}: {
  message: string;
  options: CapsuleOption[];
  dismissed: boolean;
  onSubmit: (value: string) => void;
  onDismiss: () => void;
}) {
  if (dismissed) return null;
  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.16, ease: "easeOut" }}
      className="flex justify-start"
    >
      <div className="relative max-w-[85%] rounded-lg border border-accent/30 bg-bg-elev px-3.5 py-3">
        <button
          type="button"
          onClick={onDismiss}
          className="absolute right-2 top-2 text-fg-mute hover:text-fg"
          title="关闭"
        >
          <X className="size-3.5" />
        </button>
        <p className="text-[12px] text-fg-dim">{message}</p>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {options.map((opt, i) => (
            <button
              key={i}
              type="button"
              onClick={() => onSubmit(opt.value)}
              className="rounded-full border border-accent/40 bg-accent/5 px-2.5 py-1 text-[12px] text-accent transition-colors hover:bg-accent/15"
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>
    </motion.div>
  );
}
