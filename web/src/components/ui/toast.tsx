import * as React from "react";
import * as ToastPrimitive from "@radix-ui/react-toast";
import { cn } from "@/lib/utils";

const ToastProvider = ToastPrimitive.Provider;

const ToastViewport = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Viewport>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Viewport>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Viewport
    ref={ref}
    className={cn(
      // Top-center so transient notices never cover the composer/input at the
      // bottom of the chat column.
      "fixed left-1/2 top-4 z-[100] flex max-h-screen w-80 -translate-x-1/2 flex-col gap-2 outline-none",
      className,
    )}
    {...props}
  />
));
ToastViewport.displayName = "ToastViewport";

type ToastKind = "ok" | "error" | "warn";

const kindClasses: Record<ToastKind, string> = {
  ok: "border-ok/40",
  error: "border-danger/50",
  warn: "border-warn/40",
};

const Toast = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Root> & { kind?: ToastKind }
>(({ className, kind = "error", ...props }, ref) => (
  <ToastPrimitive.Root
    ref={ref}
    className={cn(
      "rounded-md border bg-bg-elev-2 px-3.5 py-2.5 text-[13px] leading-relaxed text-fg shadow-lg",
      "data-[state=open]:animate-fade-in data-[swipe=end]:translate-x-full",
      kindClasses[kind],
      className,
    )}
    {...props}
  />
));
Toast.displayName = "Toast";

const ToastTitle = ToastPrimitive.Title;

export { ToastProvider, ToastViewport, Toast, ToastTitle };
export type { ToastKind };
