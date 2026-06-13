import { motion } from "framer-motion";

// LoadingBubble is the immediate "thinking" placeholder shown the instant a turn
// starts, before any model increment arrives. Three pulsing dots signal the
// agent has the message and is working.
export function LoadingBubble() {
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
