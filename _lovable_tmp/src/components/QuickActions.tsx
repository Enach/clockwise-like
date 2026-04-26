import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Trash2, ScrollText, ChevronDown, Loader2 } from "lucide-react";
import { api } from "@/api/client";
import { toast } from "@/hooks/useToast";

interface QuickActionsProps {
  weekISO: string;
}

export function QuickActions({ weekISO }: QuickActionsProps) {
  const qc = useQueryClient();
  const [running, setRunning] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [showAudit, setShowAudit] = useState(false);

  const audit = useQuery({
    queryKey: ["audit"],
    queryFn: () => api.getAudit(),
    enabled: showAudit,
  });

  const refreshAll = () => {
    qc.invalidateQueries({ queryKey: ["events"] });
    qc.invalidateQueries({ queryKey: ["focusBlocks"] });
    qc.invalidateQueries({ queryKey: ["audit"] });
  };

  const runFocus = async () => {
    setRunning(true);
    try {
      const res = await api.runFocus(weekISO);
      toast.success(`Created ${res.created_blocks.length} focus block${res.created_blocks.length === 1 ? "" : "s"}`);
      refreshAll();
    } catch {
      toast.error("Failed to run focus engine");
    } finally {
      setRunning(false);
    }
  };

  const clearFocus = async () => {
    if (!confirm("Clear all focus blocks for this week?")) return;
    setClearing(true);
    try {
      const { deleted } = await api.clearFocusBlocks(weekISO);
      toast.info(`Removed ${deleted} focus block${deleted === 1 ? "" : "s"}`);
      refreshAll();
    } catch {
      toast.error("Failed to clear focus blocks");
    } finally {
      setClearing(false);
    }
  };

  return (
    <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <h3 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Quick Actions
      </h3>
      <div className="space-y-2">
        <button
          onClick={runFocus}
          disabled={running}
          className="flex w-full items-center justify-between rounded-lg border border-border bg-background px-3 py-2 text-sm font-medium text-foreground transition hover:border-primary/40 hover:bg-primary-muted disabled:opacity-50"
        >
          <span className="flex items-center gap-2">
            {running ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />
            ) : (
              <Play className="h-3.5 w-3.5 text-primary" />
            )}
            Run Focus Engine
          </span>
        </button>
        <button
          onClick={clearFocus}
          disabled={clearing}
          className="flex w-full items-center justify-between rounded-lg border border-border bg-background px-3 py-2 text-sm font-medium text-foreground transition hover:border-destructive/40 hover:bg-destructive/5 disabled:opacity-50"
        >
          <span className="flex items-center gap-2">
            {clearing ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin text-destructive" />
            ) : (
              <Trash2 className="h-3.5 w-3.5 text-destructive" />
            )}
            Clear Focus Blocks
          </span>
        </button>
        <button
          onClick={() => setShowAudit((v) => !v)}
          className="flex w-full items-center justify-between rounded-lg border border-border bg-background px-3 py-2 text-sm font-medium text-foreground transition hover:bg-muted"
        >
          <span className="flex items-center gap-2">
            <ScrollText className="h-3.5 w-3.5 text-muted-foreground" />
            Audit Log
          </span>
          <ChevronDown className={`h-3.5 w-3.5 text-muted-foreground transition ${showAudit ? "rotate-180" : ""}`} />
        </button>

        {showAudit && (
          <div className="max-h-60 space-y-1.5 overflow-y-auto rounded-lg bg-muted/40 p-2">
            {audit.isLoading && (
              <p className="px-2 py-3 text-center text-xs text-muted-foreground">Loading…</p>
            )}
            {audit.data && audit.data.length === 0 && (
              <p className="px-2 py-3 text-center text-xs text-muted-foreground">No activity yet.</p>
            )}
            {audit.data?.slice(0, 10).map((e) => (
              <div key={e.id} className="rounded-md bg-card px-2.5 py-1.5 text-xs shadow-sm">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-medium text-foreground">{e.action}</span>
                  <span className="text-[10px] text-muted-foreground">
                    {new Date(e.created_at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
                  </span>
                </div>
                <p className="truncate text-[11px] text-muted-foreground">{e.details}</p>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
