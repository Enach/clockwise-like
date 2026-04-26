import type { CalendarEvent } from "@/api/types";

interface TodayAgendaProps {
  events: CalendarEvent[];
}

function fmt(iso: string) {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function TodayAgenda({ events }: TodayAgendaProps) {
  const today = new Date();
  const todays = events
    .filter((e) => {
      const d = new Date(e.start);
      return (
        d.getFullYear() === today.getFullYear() &&
        d.getMonth() === today.getMonth() &&
        d.getDate() === today.getDate()
      );
    })
    .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime());

  return (
    <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <h3 className="mb-3 flex items-center justify-between text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        <span>Today</span>
        <span className="text-[11px] font-medium normal-case tracking-normal text-muted-foreground/70">
          {today.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" })}
        </span>
      </h3>

      {todays.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-4 text-center">
          <p className="text-sm text-muted-foreground">
            No meetings today — enjoy your focus time! 🎉
          </p>
        </div>
      ) : (
        <ul className="space-y-2">
          {todays.map((e) => (
            <li key={e.id} className="flex items-start gap-3 rounded-lg p-2 transition hover:bg-muted/60">
              <span
                className="mt-1.5 h-2 w-2 shrink-0 rounded-full"
                style={{ backgroundColor: e.color || "#6366F1" }}
              />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium text-foreground">
                  {e.is_focus_block && <span className="mr-1">🎯</span>}
                  {e.title}
                </p>
                <p className="text-xs text-muted-foreground">
                  {fmt(e.start)} – {fmt(e.end)}
                </p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
