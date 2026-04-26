import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import FullCalendar from "@fullcalendar/react";
import timeGridPlugin from "@fullcalendar/timegrid";
import dayGridPlugin from "@fullcalendar/daygrid";
import interactionPlugin from "@fullcalendar/interaction";
import { useQueryClient } from "@tanstack/react-query";

import { Navbar } from "@/components/Navbar";
import { NLPBar } from "@/components/NLPBar";
import { NLPConfirmModal } from "@/components/NLPConfirmModal";
import { ConnectionStatus } from "@/components/ConnectionStatus";
import { TodayAgenda } from "@/components/TodayAgenda";
import { FocusStats } from "@/components/FocusStats";
import { QuickActions } from "@/components/QuickActions";
import { EventPopover } from "@/components/EventPopover";
import { MockBanner } from "@/components/MockBanner";

import { useSettings } from "@/hooks/useSettings";
import { useCalendarEvents } from "@/hooks/useCalendarEvents";
import { api } from "@/api/client";
import { toast } from "@/hooks/useToast";
import type { CalendarEvent, ParseResult } from "@/api/types";

function startOfWeek(d: Date) {
  const x = new Date(d);
  const day = x.getDay();
  const diff = day === 0 ? -6 : 1 - day;
  x.setDate(x.getDate() + diff);
  x.setHours(0, 0, 0, 0);
  return x;
}
function pad(n: number) {
  return n < 10 ? `0${n}` : `${n}`;
}
function dateOnly(d: Date) {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}
function fmtRange(weekStart: Date) {
  const end = new Date(weekStart);
  end.setDate(end.getDate() + 6);
  const sameMonth = end.getMonth() === weekStart.getMonth();
  const fmt = (d: Date, withMonth: boolean) =>
    d.toLocaleDateString(undefined, withMonth ? { month: "short", day: "numeric" } : { day: "numeric" });
  return sameMonth
    ? `${fmt(weekStart, true)} – ${fmt(end, false)}, ${end.getFullYear()}`
    : `${fmt(weekStart, true)} – ${fmt(end, true)}, ${end.getFullYear()}`;
}

export default function Dashboard() {
  const calRef = useRef<FullCalendar | null>(null);
  const qc = useQueryClient();

  const [weekStart, setWeekStart] = useState<Date>(() => startOfWeek(new Date()));
  const weekISO = useMemo(() => dateOnly(weekStart), [weekStart]);
  const weekEnd = useMemo(() => {
    const d = new Date(weekStart);
    d.setDate(d.getDate() + 7);
    return d;
  }, [weekStart]);

  const { data: settings } = useSettings();
  const { data: eventsRaw, refetch: refetchEvents } = useCalendarEvents(
    weekStart.toISOString(),
    weekEnd.toISOString(),
  );
  const events = Array.isArray(eventsRaw) ? eventsRaw : [];

  const [nlpInitial, setNlpInitial] = useState<string>("");
  const [nlpLoading, setNlpLoading] = useState(false);
  const [nlpError, setNlpError] = useState<string | null>(null);
  const [parseResult, setParseResult] = useState<ParseResult | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [popoverEvent, setPopoverEvent] = useState<CalendarEvent | null>(null);

  // Sync calendar API view with state
  useEffect(() => {
    const api2 = calRef.current?.getApi();
    if (!api2) return;
    api2.gotoDate(weekStart);
  }, [weekStart]);

  const handleNLP = useCallback(async (text: string) => {
    setNlpLoading(true);
    setNlpError(null);
    try {
      const res = await api.nlpParse(text);
      if (res.intent === "schedule_meeting") {
        if (!res.suggested_slots?.length) {
          setNlpError("No available slots found for that request.");
        } else {
          setParseResult(res);
        }
      } else if (res.intent === "schedule_focus") {
        toast.success("Scheduling focus time…");
        await api.runFocus(weekISO);
        qc.invalidateQueries({ queryKey: ["events"] });
        qc.invalidateQueries({ queryKey: ["focusBlocks"] });
        toast.success("Focus time scheduled");
      } else {
        setNlpError(res.error || "Sorry, I couldn't understand that request.");
      }
    } catch {
      setNlpError("Network error. Please try again.");
    } finally {
      setNlpLoading(false);
    }
  }, [qc, weekISO]);

  const handleConfirm = useCallback(async (slotIndex: number) => {
    if (!parseResult) return;
    setConfirming(true);
    try {
      const ev = await api.nlpConfirm(parseResult, slotIndex);
      toast.success(`Scheduled "${ev.title}"`);
      setParseResult(null);
      setNlpInitial("");
      qc.invalidateQueries({ queryKey: ["events"] });
    } catch {
      toast.error("Failed to schedule meeting");
    } finally {
      setConfirming(false);
    }
  }, [parseResult, qc]);

  const fcEvents = useMemo(
    () =>
      events.map((e) => {
        const isFocus =
          e.is_focus_block ||
          (settings && e.title.toLowerCase().includes((settings.focus_label || "focus").toLowerCase()));
        const color = isFocus ? settings?.focus_color || e.color || "#7C3AED" : e.color || "#3B82F6";
        return {
          id: e.id,
          title: isFocus ? `🎯 ${e.title}` : e.title,
          start: e.start,
          end: e.end,
          backgroundColor: color,
          borderColor: color,
          editable: !isFocus,
          extendedProps: { raw: e, isFocus },
        };
      }),
    [events, settings],
  );

  return (
    <div className="min-h-screen bg-muted/30">
      <MockBanner />
      <Navbar
        weekLabel={fmtRange(weekStart)}
        onPrevWeek={() => setWeekStart((w) => {
          const d = new Date(w); d.setDate(d.getDate() - 7); return d;
        })}
        onNextWeek={() => setWeekStart((w) => {
          const d = new Date(w); d.setDate(d.getDate() + 7); return d;
        })}
        onToday={() => setWeekStart(startOfWeek(new Date()))}
      />

      <main className="mx-auto w-full max-w-[1600px] space-y-4 p-4 sm:p-6">
        <NLPBar
          initialValue={nlpInitial}
          loading={nlpLoading}
          error={nlpError}
          onSubmit={handleNLP}
        />

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_320px]">
          {/* Calendar */}
          <section className="rounded-2xl border border-border bg-card p-3 shadow-sm sm:p-4">
            <FullCalendar
              ref={calRef}
              plugins={[timeGridPlugin, dayGridPlugin, interactionPlugin]}
              initialView="timeGridWeek"
              initialDate={weekStart}
              headerToolbar={{ left: "", center: "", right: "timeGridWeek,timeGridDay" }}
              firstDay={1}
              allDaySlot={false}
              nowIndicator
              slotDuration="00:30:00"
              slotMinTime="07:00:00"
              slotMaxTime="21:00:00"
              expandRows
              height="auto"
              contentHeight={680}
              businessHours={
                settings
                  ? {
                      daysOfWeek: [1, 2, 3, 4, 5],
                      startTime: settings.work_start,
                      endTime: settings.work_end,
                    }
                  : undefined
              }
              events={fcEvents}
              selectable
              select={(arg) => {
                const day = arg.start.toLocaleDateString(undefined, { weekday: "long" });
                const time = arg.start.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
                setNlpInitial(`Schedule a 30 min meeting on ${day} at ${time}`);
                arg.view.calendar.unselect();
              }}
              eventClick={(arg) => {
                const raw = arg.event.extendedProps.raw as CalendarEvent | undefined;
                if (raw) setPopoverEvent(raw);
              }}
              datesSet={(arg) => {
                const newStart = startOfWeek(arg.start);
                if (newStart.getTime() !== weekStart.getTime()) {
                  setWeekStart(newStart);
                }
              }}
            />
          </section>

          {/* Sidebar */}
          <aside className="space-y-4">
            <ConnectionStatus />
            <TodayAgenda events={events} />
            {settings && (
              <FocusStats
                weekISO={weekISO}
                dailyTargetMinutes={settings.focus_daily_target_minutes}
                focusColor={settings.focus_color}
              />
            )}
            <QuickActions weekISO={weekISO} />
          </aside>
        </div>
      </main>

      {parseResult && (
        <NLPConfirmModal
          parseResult={parseResult}
          loading={confirming}
          onClose={() => setParseResult(null)}
          onConfirm={handleConfirm}
        />
      )}
      {popoverEvent && (
        <EventPopover event={popoverEvent} onClose={() => setPopoverEvent(null)} />
      )}
    </div>
  );
}
