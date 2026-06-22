import * as React from "react";
import { motion } from "framer-motion";
import { ImageUp } from "lucide-react";
import { BrandMark } from "@/components/brand-mark";
import { useApp } from "@/store/context";
import { Composer } from "./composer";

// OnboardingHero is the immersive first screen: a Google-style centered input
// with the brand above it and gentle upload guidance below. The whole region is
// a drop target — dragging an image anywhere over it reveals the upload overlay
// — so the two ways in (type a request / drop an image) are both one gesture
// away. It renders only while the workspace is empty; the first message turns
// the layout into a normal chat stream.
export function OnboardingHero() {
  const app = useApp();
  const [dragging, setDragging] = React.useState(false);
  // Depth counter so nested dragenter/leave events don't flicker the overlay.
  const dragDepth = React.useRef(0);

  const onDrop = async (e: React.DragEvent) => {
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (e.dataTransfer.files?.length) await app.uploadFiles(e.dataTransfer.files);
  };

  return (
    <div
      onDragEnter={(e) => { e.preventDefault(); dragDepth.current++; setDragging(true); }}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={(e) => { e.preventDefault(); dragDepth.current--; if (dragDepth.current <= 0) setDragging(false); }}
      onDrop={onDrop}
      className="relative flex h-full flex-col items-center justify-center gap-16 px-4"
    >
      {/* Brand reveal */}
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: "easeOut" }}
        className="flex flex-col items-center gap-3.5 text-center"
      >
        <BrandMark className="size-10 text-accent" />
        <div>
          <h1 className="text-lg font-semibold tracking-[0.18em] text-fg">GAME ASSET STUDIO</h1>
          <p className="mt-2 text-[13px] leading-relaxed text-fg-mute">
            上传一张游戏素材图，或直接描述你想做的事
          </p>
        </div>
      </motion.div>

      {/* Centered input */}
      <motion.div
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: "easeOut", delay: 0.1 }}
        className="w-full max-w-xl"
      >
        <Composer onboarding />
      </motion.div>

      {/* Drag-to-upload overlay */}
      {dragging && (
        <div className="absolute inset-3 z-20 grid place-items-center rounded-2xl border-2 border-dashed border-accent/60 bg-bg/80 backdrop-blur-sm transition-all duration-200 ease-out">
          <div className="flex flex-col items-center gap-2.5 text-accent">
            <ImageUp className="size-8" />
            <p className="text-sm font-medium">松手即可上传图片</p>
          </div>
        </div>
      )}
    </div>
  );
}
