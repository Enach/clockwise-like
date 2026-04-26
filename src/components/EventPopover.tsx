import { X, Clock, Users } from "lucide-react";
import type { CalendarEvent } from "@/api/types";

interface EventPopoverProps {
  event: CalendarEvent;
  onClose: () => void;
}

function fmt(iso: string) {
  return new Date(iso).toLocaleString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function EventPopover({ event, onClose }: EventPopoverProps) {
  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/30 p-4"
      onClick={onClose}
    >
      <div
        className="w-full max-w-sm rounded-2xl bg-card p-5 shadow-xl ring-1 ring-border"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-2">
            <span
              className="mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full"
              style={{ backgroundColor: event.color || "#6366F1" }}
            />
            <h3 className="text-base font-semibold leading-tight text-foreground">
              {event.is_focus_block && <span className="mr-1">🎯</span>}
              {event.title}
            </h3>
          </div>
          <button
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="mt-3 space-y-2 text-sm">
          <div className="flex items-center gap-2 text-muted-foreground">
            <Clock className="h-3.5 w-3.5" />
            <span>
              {fmt(event.start)} → {fmt(event.end)}
            </span>
          </div>
          {!!event.attendees?.length && (
            <div className="flex items-start gap-2 text-muted-foreground">
              <Users className="mt-0.5 h-3.5 w-3.5" />
              <span className="break-all">{event.attendees.join(", ")}</span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
