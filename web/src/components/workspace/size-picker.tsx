import * as React from "react";
import type { Channel, SizePreset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import * as api from "@/lib/api";
import { cn } from "@/lib/utils";

interface Chosen {
  id: string;
  label: string;
}

// SizePicker selects platform crop sizes (channel → asset type → size) and runs
// the crop against one or many source assets. Group tabs filter channels.
export function SizePicker({
  assetIds,
  onOpenChange,
}: {
  assetIds: string[] | null;
  onOpenChange: (open: boolean) => void;
}) {
  const app = useApp();
  const [channels, setChannels] = React.useState<Channel[]>([]);
  const [group, setGroup] = React.useState("all");
  const [activeChannel, setActiveChannel] = React.useState<string | null>(null);
  const [chosen, setChosen] = React.useState<Map<string, Chosen>>(new Map());
  const [running, setRunning] = React.useState(false);

  React.useEffect(() => {
    api.listPlatforms().then(setChannels).catch(() => setChannels([]));
  }, []);

  React.useEffect(() => {
    if (assetIds) {
      setChosen(new Map());
      setGroup("all");
    }
  }, [assetIds]);

  const groups = React.useMemo(() => {
    const set = new Set<string>();
    for (const c of channels) if (c.group) set.add(c.group);
    return ["all", ...set];
  }, [channels]);

  const visible = React.useMemo(
    () => channels.filter((c) => group === "all" || c.group === group),
    [channels, group],
  );

  const channel = visible.find((c) => c.id === activeChannel) || visible[0];

  const toggleSize = (sz: SizePreset, ch: Channel) => {
    setChosen((prev) => {
      const next = new Map(prev);
      if (next.has(sz.id)) next.delete(sz.id);
      else next.set(sz.id, { id: sz.id, label: `${ch.name} · ${sz.name}` });
      return next;
    });
  };

  const run = async () => {
    if (!assetIds || chosen.size === 0) return;
    setRunning(true);
    const sizeIds = [...chosen.keys()];
    try {
      for (const aid of assetIds) {
        await api.crop(app.state.sessionId, aid, sizeIds, app.state.lossless);
      }
      onOpenChange(false);
      await app.refreshWorkspace(app.state.sessionId);
      app.toast(`已切 ${sizeIds.length} 个尺寸 × ${assetIds.length} 张`, "ok");
    } catch (e) {
      app.toast("切尺寸失败：" + (e as Error).message);
    } finally {
      setRunning(false);
    }
  };

  return (
    <Dialog open={!!assetIds} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(720px,94vw)]">
        <DialogHeader>
          <DialogTitle>
            选择平台尺寸{assetIds && assetIds.length > 1 ? ` · ${assetIds.length} 张` : ""}
          </DialogTitle>
        </DialogHeader>

        <Tabs value={group} onValueChange={setGroup}>
          <TabsList>
            {groups.map((g) => (
              <TabsTrigger key={g} value={g}>{g === "all" ? "全部" : g}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        <div className="mt-3 grid grid-cols-[180px_1fr] gap-3" style={{ minHeight: 320 }}>
          <div className="space-y-0.5 overflow-y-auto border-r border-line pr-2">
            {visible.map((c) => {
              const n = countChosen(c, chosen);
              return (
                <button
                  key={c.id}
                  onClick={() => setActiveChannel(c.id)}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[13px] transition-colors",
                    channel?.id === c.id ? "bg-bg-elev-2 text-fg" : "text-fg-dim hover:bg-bg-elev",
                  )}
                >
                  <span className="truncate">{c.name}</span>
                  {n > 0 && <span className="ml-auto rounded-full bg-accent/20 px-1.5 text-[10px] text-accent">{n}</span>}
                </button>
              );
            })}
          </div>

          <div className="overflow-y-auto pr-1">
            {channel?.assetTypes.map((at) => {
              const sizes = at.sizes.filter((s) => s.producible);
              if (sizes.length === 0) return null;
              return (
                <div key={at.type} className="mb-4">
                  <div className="mb-2 text-xs font-medium text-fg-mute">{at.name}</div>
                  <div className="flex flex-wrap gap-1.5">
                    {sizes.map((sz) => (
                      <button
                        key={sz.id}
                        onClick={() => toggleSize(sz, channel)}
                        className={cn(
                          "rounded-md border px-2.5 py-1 text-xs transition-colors",
                          chosen.has(sz.id)
                            ? "border-accent bg-accent/15 text-accent"
                            : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
                        )}
                      >
                        {sz.name} <span className="tabular-nums text-fg-mute">{sz.width}×{sz.height}</span>
                      </button>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        <div className="mt-3 flex items-center gap-3 border-t border-line pt-3">
          <span className="text-xs text-fg-dim">已选 {chosen.size} 个尺寸</span>
          <Button className="ml-auto" size="sm" disabled={chosen.size === 0 || running} onClick={run}>
            {running ? "处理中…" : "开始裁剪"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function countChosen(c: Channel, chosen: Map<string, Chosen>): number {
  let n = 0;
  for (const at of c.assetTypes) for (const s of at.sizes) if (chosen.has(s.id)) n++;
  return n;
}
