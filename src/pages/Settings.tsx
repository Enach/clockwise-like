import { useEffect, useState } from "react";
import { Loader2, Save } from "lucide-react";
import { Navbar } from "@/components/Navbar";
import { ConnectionStatus } from "@/components/ConnectionStatus";
import { MockBanner } from "@/components/MockBanner";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";
import { toast } from "@/hooks/useToast";
import type { Settings } from "@/api/types";

const TIMEZONES = [
  "UTC",
  "Europe/Paris",
  "Europe/London",
  "America/New_York",
  "America/Chicago",
  "America/Denver",
  "America/Los_Angeles",
  "America/Toronto",
  "Asia/Tokyo",
  "Asia/Shanghai",
  "Asia/Singapore",
  "Australia/Sydney",
  "Pacific/Auckland",
];
const PROVIDERS = ["openai", "anthropic", "ollama"];

function fmtHM(min: number) {
  const h = Math.floor(min / 60);
  const m = min % 60;
  if (h === 0) return `${m} min`;
  if (m === 0) return `${h} h`;
  return `${h} h ${m} min`;
}

function Section({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-border bg-card p-5 shadow-sm sm:p-6">
      <div className="mb-4">
        <h2 className="text-base font-semibold tracking-tight text-foreground">{title}</h2>
        {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
      </div>
      {children}
    </section>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-foreground">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[11px] text-muted-foreground">{hint}</span>}
    </label>
  );
}

const inputCls =
  "h-10 w-full rounded-lg border border-input bg-background px-3 text-sm text-foreground transition placeholder:text-muted-foreground focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/20";

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 select-none">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative h-5 w-9 shrink-0 rounded-full transition ${checked ? "bg-primary" : "bg-muted"}`}
      >
        <span
          className={`absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition ${
            checked ? "left-[18px]" : "left-0.5"
          }`}
        />
      </button>
      {label && <span className="text-sm text-foreground">{label}</span>}
    </label>
  );
}

export default function SettingsPage() {
  const { data, isLoading } = useSettings();
  const update = useUpdateSettings();
  const [draft, setDraft] = useState<Settings | null>(null);

  useEffect(() => {
    if (data && !draft) setDraft({ ...data });
  }, [data, draft]);

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => {
    setDraft((d) => (d ? { ...d, [k]: v } : d));
  };

  const save = async () => {
    if (!draft) return;
    try {
      await update.mutateAsync(draft);
      toast.success("Settings saved");
    } catch {
      toast.error("Failed to save settings");
    }
  };

  if (isLoading || !draft) {
    return (
      <div className="min-h-screen bg-muted/30">
        <Navbar />
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-muted/30">
      <MockBanner />
      <Navbar />
      <main className="mx-auto w-full max-w-3xl space-y-4 p-4 sm:p-6">
        <div className="mb-2 flex items-end justify-between gap-3">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-foreground">Settings</h1>
            <p className="mt-1 text-sm text-muted-foreground">Configure how Clockwise-like manages your calendar.</p>
          </div>
          <button
            onClick={save}
            disabled={update.isPending}
            className="flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground shadow-sm transition hover:bg-primary/90 disabled:opacity-50"
          >
            {update.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            Save changes
          </button>
        </div>

        <Section title="Working Hours" description="When you're available for meetings.">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Field label="Start">
              <input
                type="time"
                value={draft.work_start}
                onChange={(e) => set("work_start", e.target.value)}
                className={inputCls}
              />
            </Field>
            <Field label="End">
              <input
                type="time"
                value={draft.work_end}
                onChange={(e) => set("work_end", e.target.value)}
                className={inputCls}
              />
            </Field>
            <Field label="Timezone">
              <select
                value={draft.timezone}
                onChange={(e) => set("timezone", e.target.value)}
                className={inputCls}
              >
                {TIMEZONES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </Section>

        <Section title="Focus Time" description="Auto-scheduled deep work blocks.">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Min block (minutes)">
              <input
                type="number"
                min={15}
                step={15}
                value={draft.focus_min_block_minutes}
                onChange={(e) => set("focus_min_block_minutes", Number(e.target.value))}
                className={inputCls}
              />
            </Field>
            <Field label="Max block (minutes)">
              <input
                type="number"
                min={15}
                step={15}
                value={draft.focus_max_block_minutes}
                onChange={(e) => set("focus_max_block_minutes", Number(e.target.value))}
                className={inputCls}
              />
            </Field>
            <Field label="Daily target (minutes)" hint={fmtHM(draft.focus_daily_target_minutes)}>
              <input
                type="number"
                min={0}
                step={15}
                value={draft.focus_daily_target_minutes}
                onChange={(e) => set("focus_daily_target_minutes", Number(e.target.value))}
                className={inputCls}
              />
            </Field>
            <Field label="Label">
              <input
                type="text"
                value={draft.focus_label}
                onChange={(e) => set("focus_label", e.target.value)}
                className={inputCls}
              />
            </Field>
            <Field label="Color">
              <div className="flex items-center gap-2">
                <input
                  type="color"
                  value={draft.focus_color}
                  onChange={(e) => set("focus_color", e.target.value)}
                  className="h-10 w-12 cursor-pointer rounded-lg border border-input bg-background"
                />
                <input
                  type="text"
                  value={draft.focus_color}
                  onChange={(e) => set("focus_color", e.target.value)}
                  className={inputCls}
                />
              </div>
            </Field>
            <div className="sm:col-span-2">
              <Toggle
                checked={draft.auto_schedule_enabled}
                onChange={(v) => set("auto_schedule_enabled", v)}
                label="Auto-schedule focus time"
              />
            </div>
            {draft.auto_schedule_enabled && (
              <div className="sm:col-span-2">
                <Field
                  label="Cron schedule"
                  hint="e.g. 0 7 * * 1-5 = weekdays at 7am"
                >
                  <input
                    type="text"
                    value={draft.auto_schedule_cron}
                    onChange={(e) => set("auto_schedule_cron", e.target.value)}
                    className={inputCls}
                  />
                </Field>
              </div>
            )}
          </div>
        </Section>

        <Section title="Lunch Break">
          <div className="space-y-4">
            <Toggle
              checked={draft.protect_lunch}
              onChange={(v) => set("protect_lunch", v)}
              label="Protect lunch break"
            />
            {draft.protect_lunch && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field label="Start">
                  <input
                    type="time"
                    value={draft.lunch_start}
                    onChange={(e) => set("lunch_start", e.target.value)}
                    className={inputCls}
                  />
                </Field>
                <Field label="End">
                  <input
                    type="time"
                    value={draft.lunch_end}
                    onChange={(e) => set("lunch_end", e.target.value)}
                    className={inputCls}
                  />
                </Field>
              </div>
            )}
          </div>
        </Section>

        <Section title="Meeting Buffers" description="Add breathing room between meetings.">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Buffer before (minutes)">
              <input
                type="number"
                min={0}
                step={5}
                value={draft.buffer_before_minutes}
                onChange={(e) => set("buffer_before_minutes", Number(e.target.value))}
                className={inputCls}
              />
            </Field>
            <Field label="Buffer after (minutes)">
              <input
                type="number"
                min={0}
                step={5}
                value={draft.buffer_after_minutes}
                onChange={(e) => set("buffer_after_minutes", Number(e.target.value))}
                className={inputCls}
              />
            </Field>
          </div>
        </Section>

        <Section title="Meeting Compression">
          <Toggle
            checked={draft.compression_enabled}
            onChange={(v) => set("compression_enabled", v)}
            label="Enable meeting compression"
          />
          <p className="mt-2 text-xs text-muted-foreground">
            When enabled, suggests rearranging your internal meetings to create larger Focus Time blocks.
          </p>
        </Section>

        <Section title="Google Calendar">
          <div className="space-y-4">
            <ConnectionStatus compact />
            <Field label="Calendar ID">
              <input
                type="text"
                value={draft.calendar_id ?? "primary"}
                onChange={(e) => set("calendar_id", e.target.value)}
                className={inputCls}
                placeholder="primary"
              />
            </Field>
          </div>
        </Section>

        <Section title="AI / NLP">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Provider">
              <select
                value={draft.llm_provider}
                onChange={(e) => set("llm_provider", e.target.value)}
                className={inputCls}
              >
                {PROVIDERS.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Model">
              <input
                type="text"
                value={draft.llm_model}
                onChange={(e) => set("llm_model", e.target.value)}
                className={inputCls}
              />
            </Field>
            <Field label="API key">
              <input
                type="password"
                value={draft.llm_api_key ?? ""}
                onChange={(e) => set("llm_api_key", e.target.value)}
                className={inputCls}
                placeholder="••••••••"
              />
            </Field>
            {draft.llm_provider === "ollama" && (
              <Field label="Base URL">
                <input
                  type="text"
                  value={draft.llm_base_url ?? ""}
                  onChange={(e) => set("llm_base_url", e.target.value)}
                  className={inputCls}
                  placeholder="http://localhost:11434"
                />
              </Field>
            )}
          </div>
        </Section>
      </main>
    </div>
  );
}
