import { useState } from "react";
import { X, Calendar, Clock, Users, Loader2 } from "lucide-react";
import type { ParseResult } from "@/api/types";

interface NLPConfirmModalProps {
  parseResult: ParseResult;
  loading?: boolean;
  onClose: () => void;
  onConfirm: (slotIndex: number) => void;
}

function fmtDay(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" });
}
function fmtTime(iso: string) {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function NLPConfirmModal({ parseResult, loading, onClose, onConfirm }: NLPConfirmModalProps) {
  const slots = (parseResult.suggested_slots ?? []).slice(0, 3);
  const [selected, setSelected] = useState(0);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
    >
      <div
        className="w-full max-w-lg rounded-2xl bg-card shadow-xl ring-1 ring-border"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between border-b border-border p-5">
          <div>
            <h2 className="text-base font-semibold tracking-tight">Confirm meeting</h2>
            <p className="mt-0.5 text-xs text-muted-foreground">Pick a time slot to schedule.</p>
          </div>
          <button
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground transition hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 p-5">
          <div className="space-y-1.5 rounded-xl bg-muted/50 p-3 text-xs">
            <div className="flex items-center gap-2 text-foreground">
              <Calendar className="h-3.5 w-3.5 text-muted-foreground" />
              <span className="font-semibold">{parseResult.title || "Untitled meeting"}</span>
            </div>
            <div className="flex items-center gap-2 text-muted-foreground">
              <Clock className="h-3.5 w-3.5" />
              <span>{parseResult.duration_minutes ?? 30} minutes</span>
            </div>
            {!!parseResult.attendees?.length && (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Users className="h-3.5 w-3.5" />
                <span className="truncate">{parseResult.attendees.join(", ")}</span>
              </div>
            )}
          </div>

          {slots.length === 0 ? (
            <p className="rounded-lg border border-dashed border-border p-4 text-center text-sm text-muted-foreground">
              No available slots found.
            </p>
          ) : (
            <div className="space-y-2">
              {slots.map((s, i) => {
                const active = selected === i;
                return (
                  <button
                    key={i}
                    onClick={() => setSelected(i)}
                    className={`w-full rounded-xl border p-3 text-left transition ${
                      active
                        ? "border-primary bg-primary-muted ring-2 ring-primary/20"
                        : "border-border bg-background hover:border-primary/40"
                    }`}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <p className="text-sm font-semibold text-foreground">{fmtDay(s.start)}</p>
                        <p className="text-xs text-muted-foreground">
                          {fmtTime(s.start)} – {fmtTime(s.end)}
                        </p>
                      </div>
                      <div className="flex flex-col items-end gap-1">
                        <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                          Score
                        </span>
                        <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full bg-primary"
                            style={{ width: `${Math.round(s.score * 100)}%` }}
                          />
                        </div>
                      </div>
                    </div>
                    {!!s.reasons?.length && (
                      <div className="mt-2 flex flex-wrap gap-1">
                        {s.reasons.map((r) => (
                          <span
                            key={r}
                            className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground"
                          >
                            {r}
                          </span>
                        ))}
                      </div>
                    )}
                  </button>
                );
              })}
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border p-4">
          <button
            onClick={onClose}
            className="rounded-lg px-3 py-1.5 text-sm font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground"
          >
            Cancel
          </button>
          <button
            onClick={() => onConfirm(selected)}
            disabled={loading || slots.length === 0}
            className="flex items-center gap-1.5 rounded-lg bg-primary px-4 py-1.5 text-sm font-semibold text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
          >
            {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Confirm
          </button>
        </div>
      </div>
    </div>
  );
}
