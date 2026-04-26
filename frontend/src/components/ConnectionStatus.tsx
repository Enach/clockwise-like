import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import { Loader2, LogIn, LogOut } from "lucide-react";
import { toast } from "@/hooks/useToast";
import { useState } from "react";

export function ConnectionStatus({ compact = false }: { compact?: boolean }) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ["auth"], queryFn: () => api.authStatus() });
  const [busy, setBusy] = useState(false);

  const handleConnect = () => {
    window.location.href = api.authConnectUrl();
  };
  const handleDisconnect = async () => {
    setBusy(true);
    try {
      await api.authDisconnect();
      toast.success("Disconnected from Google Calendar");
      qc.invalidateQueries({ queryKey: ["auth"] });
    } catch {
      toast.error("Failed to disconnect");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className={`rounded-xl border border-border bg-card ${compact ? "p-3" : "p-4"} shadow-sm`}>
      {!compact && (
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Google Calendar
        </h3>
      )}
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          {isLoading ? (
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          ) : (
            <span
              className={`h-2.5 w-2.5 shrink-0 rounded-full ${
                data?.connected ? "bg-success" : "bg-destructive"
              } ring-2 ring-offset-2 ring-offset-card ${
                data?.connected ? "ring-success/30" : "ring-destructive/30"
              }`}
            />
          )}
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-foreground">
              {data?.connected ? data.email || "Connected" : "Not connected"}
            </p>
            <p className="text-[11px] text-muted-foreground">
              {data?.connected ? "Syncing in real time" : "Connect to enable scheduling"}
            </p>
          </div>
        </div>
        {data?.connected ? (
          <button
            onClick={handleDisconnect}
            disabled={busy}
            className="flex shrink-0 items-center gap-1.5 rounded-lg border border-border px-2.5 py-1 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:opacity-50"
          >
            <LogOut className="h-3 w-3" />
            Disconnect
          </button>
        ) : (
          <button
            onClick={handleConnect}
            className="flex shrink-0 items-center gap-1.5 rounded-lg bg-primary px-2.5 py-1 text-xs font-semibold text-primary-foreground transition hover:bg-primary/90"
          >
            <LogIn className="h-3 w-3" />
            Connect
          </button>
        )}
      </div>
    </div>
  );
}
