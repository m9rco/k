import * as React from "react";
import { Toast, ToastProvider, ToastTitle, ToastViewport, type ToastKind } from "@/components/ui/toast";

interface ToastItem {
  id: number;
  message: string;
  kind: ToastKind;
}

interface ToastCtx {
  toast: (message: string, kind?: ToastKind) => void;
}

const Ctx = React.createContext<ToastCtx | null>(null);

export function useToast() {
  const ctx = React.useContext(Ctx);
  if (!ctx) throw new Error("useToast must be used within ToastHost");
  return ctx;
}

let nextId = 1;

export function ToastHost({ children }: { children: React.ReactNode }) {
  const [items, setItems] = React.useState<ToastItem[]>([]);

  const toast = React.useCallback((message: string, kind: ToastKind = "error") => {
    const id = nextId++;
    setItems((cur) => [...cur, { id, message, kind }]);
  }, []);

  const remove = React.useCallback((id: number) => {
    setItems((cur) => cur.filter((t) => t.id !== id));
  }, []);

  return (
    <Ctx.Provider value={{ toast }}>
      <ToastProvider swipeDirection="right" duration={4200}>
        {children}
        {items.map((t) => (
          <Toast key={t.id} kind={t.kind} onOpenChange={(open) => !open && remove(t.id)}>
            <ToastTitle>{t.message}</ToastTitle>
          </Toast>
        ))}
        <ToastViewport />
      </ToastProvider>
    </Ctx.Provider>
  );
}
