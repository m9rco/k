import * as React from "react";
import { useAppController } from "./controller";

type Controller = ReturnType<typeof useAppController>;

const AppCtx = React.createContext<Controller | null>(null);

export function AppProvider({ children }: { children: React.ReactNode }) {
  const ctrl = useAppController();
  return <AppCtx.Provider value={ctrl}>{children}</AppCtx.Provider>;
}

export function useApp() {
  const ctx = React.useContext(AppCtx);
  if (!ctx) throw new Error("useApp must be used within AppProvider");
  return ctx;
}
