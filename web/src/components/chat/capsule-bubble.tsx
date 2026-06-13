import * as React from "react";
import { motion } from "framer-motion";
import { Pencil, CornerDownLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { CapsuleOption } from "@/store/types";

// CapsuleBubble renders a structured clarify prompt: a question plus 2-4 option
// chips. Clicking a chip sends its value immediately; the pencil toggles an
// inline editor pre-filled with the option's editable hint so the user can
// refine it before sending. Once answered, the whole capsule is disabled.
export function CapsuleBubble({
  question,
  options,
  answered,
  onSubmit,
}: {
  question: string;
  options: CapsuleOption[];
  answered: boolean;
  onSubmit: (value: string) => void;
}) {
  const [editing, setEditing] = React.useState<number | null>(null);
  const [draft, setDraft] = React.useState("");
  const inputRef = React.useRef<HTMLInputElement>(null);

  React.useEffect(() => {
    if (editing !== null) inputRef.current?.focus();
  }, [editing]);

  const startEdit = (i: number, opt: CapsuleOption) => {
    setEditing(i);
    setDraft(opt.editableHint ?? opt.value);
  };

  const submitDraft = () => {
    const v = draft.trim();
    if (v) onSubmit(v);
  };

  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.16, ease: "easeOut" }}
      className="flex justify-start"
    >
      <div
        className={cn(
          "max-w-[85%] rounded-lg border border-line bg-bg-elev px-3.5 py-3",
          answered && "opacity-60",
        )}
      >
        <p className="text-[13px] leading-relaxed text-fg">{question}</p>
        <div className="mt-2.5 flex flex-col gap-1.5">
          {options.map((opt, i) =>
            editing === i ? (
              <div key={i} className="flex items-center gap-1.5">
                <input
                  ref={inputRef}
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") submitDraft();
                    if (e.key === "Escape") setEditing(null);
                  }}
                  disabled={answered}
                  className="h-8 flex-1 rounded-md border border-line bg-bg px-2.5 text-[13px] text-fg outline-none focus:border-accent"
                />
                <Button size="icon-sm" variant="default" onClick={submitDraft} disabled={answered} title="发送">
                  <CornerDownLeft />
                </Button>
              </div>
            ) : (
              <div key={i} className="flex items-center gap-1.5">
                <button
                  type="button"
                  disabled={answered}
                  onClick={() => onSubmit(opt.value)}
                  className="flex-1 rounded-md border border-line bg-bg px-2.5 py-1.5 text-left text-[13px] text-fg-dim transition-colors hover:border-accent hover:text-fg disabled:cursor-not-allowed"
                >
                  {opt.label}
                </button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  onClick={() => startEdit(i, opt)}
                  disabled={answered}
                  title="编辑后发送"
                >
                  <Pencil />
                </Button>
              </div>
            ),
          )}
        </div>
      </div>
    </motion.div>
  );
}
