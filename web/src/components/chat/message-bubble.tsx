import { motion } from "framer-motion";
import { cn } from "@/lib/utils";

// MessageBubble renders a user or assistant message. Assistant text streams via
// the controller's typewriter; a thin caret shows while streaming.
export function MessageBubble({
  role,
  text,
  streaming,
}: {
  role: "user" | "assistant";
  text: string;
  streaming?: boolean;
}) {
  const isUser = role === "user";
  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.16, ease: "easeOut" }}
      className={cn("flex", isUser ? "justify-end" : "justify-start")}
    >
      <div
        className={cn(
          "max-w-[85%] whitespace-pre-wrap rounded-lg px-3.5 py-2.5 text-[13px] leading-relaxed",
          isUser ? "bg-accent/15 text-fg" : "bg-bg-elev text-fg",
        )}
      >
        {text}
        {streaming && <span className="ml-0.5 inline-block h-3.5 w-px animate-pulse bg-accent align-middle" />}
      </div>
    </motion.div>
  );
}
