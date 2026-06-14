import { motion } from "framer-motion";
import type { WaitLevel } from "@/store/types";

// LoadingBubble is the tiered wait-state placeholder shown between turn_start
// and the first model increment. It never blanks the UI:
//   - P1 (default): a lightweight micro-hint — a soft pulsing dot beside
//     "正在启动深度思考…" — so the user immediately perceives the agent is
//     spinning up its reasoning, kept to a millisecond-scale impression.
//   - P2 (fallback): a more explicit static spinner, shown only when the turn is
//     known non-streaming (backend signal) or no increment arrived within the P1
//     timeout. Same bubble instance switches level to avoid layout jumps.
export function LoadingBubble({  }: { level: WaitLevel }) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.16, ease: "easeOut" }}
      className="flex justify-start"
    >
      <div className="flex items-center gap-1 rounded-lg bg-bg-elev px-3.5 py-3">
        {[0, 1, 2].map((i) => (
          <span
            key={i}
            className="size-1.5 animate-bounce rounded-full bg-fg-mute"
            style={{ animationDelay: `${i * 0.15}s` }}
          />
        ))}
      </div>
    </motion.div>
  );
}
