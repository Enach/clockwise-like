import { useFocusBlocks } from "@/hooks/useFocusBlocks";
import { Target } from "lucide-react";

interface FocusStatsProps {
  weekISO: string;
  dailyTargetMinutes: number;
  focusColor: string;
}

function fmtHM(min: number) {
  const h = Math.floor(min / 60);
  const m = min % 60;
  if (h === 0) return `${m} min`;
  if (m === 0) return `${h} h`;
  return `${h} h ${m} min`;
}

const DAYS = ["Mon", "Tue", "Wed", "Thu", "Fri"];

export function FocusStats({ weekISO, dailyTargetMinutes, focusColor }: FocusStatsProps) {
  const { data } = useFocusBlocks(weekISO);
  const blocks = Array.isArray(data) ? data : [];

  const weeklyTarget = dailyTargetMinutes * 5;
  const totalMin = blocks.reduce(
    (acc, b) => acc + Math.max(0, (new Date(b.end_time).getTime() - new Date(b.start_time).getTime()) / 60_000),
    0,
  );
  const pct = Math.min(100, Math.round((totalMin / Math.max(1, weeklyTarget)) * 100));

  // per-weekday minutes
  const weekStart = new Date(weekISO);
  const perDay = new Array(5).fill(0);
  blocks.forEach((b) => {
    const d = new Date(b.start_time);
    const offset = Math.floor((d.getTime() - weekStart.getTime()) / (24 * 3600_000));
    if (offset >= 0 && offset < 5) {
      perDay[offset] += (new Date(b.end_time).getTime() - new Date(b.start_time).getTime()) / 60_000;
    }
  });
  const dayMax = Math.max(dailyTargetMinutes, ...perDay, 1);

  return (
    <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Focus This Week
        </h3>
        <div
          className="flex h-6 w-6 items-center justify-center rounded-md text-white"
          style={{ backgroundColor: focusColor }}
        >
          <Target className="h-3.5 w-3.5" />
        </div>
      </div>

      <p className="text-2xl font-semibold tracking-tight text-foreground">{fmtHM(totalMin)}</p>
      <p className="mt-0.5 text-xs text-muted-foreground">
        of {fmtHM(weeklyTarget)} target ({pct}%)
      </p>

      <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full transition-all"
          style={{ width: `${pct}%`, backgroundColor: focusColor }}
        />
      </div>

      <div className="mt-4 flex items-end justify-between gap-1.5">
        {DAYS.map((d, i) => {
          const h = Math.max(6, Math.round((perDay[i] / dayMax) * 48));
          return (
            <div key={d} className="flex flex-1 flex-col items-center gap-1">
              <div className="flex h-12 w-full items-end">
                <div
                  className="w-full rounded-md transition-all"
                  style={{ height: `${h}px`, backgroundColor: perDay[i] > 0 ? focusColor : "hsl(var(--muted))" }}
                />
              </div>
              <span className="text-[10px] font-medium text-muted-foreground">{d}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
