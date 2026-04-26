import { Calendar, Settings as SettingsIcon, ChevronLeft, ChevronRight } from "lucide-react";
import { Link } from "react-router-dom";

interface NavbarProps {
  weekLabel?: string;
  onPrevWeek?: () => void;
  onNextWeek?: () => void;
  onToday?: () => void;
  showSettingsLink?: boolean;
}

export function Navbar({ weekLabel, onPrevWeek, onNextWeek, onToday, showSettingsLink = true }: NavbarProps) {
  return (
    <header className="sticky top-0 z-30 border-b border-border bg-card/80 backdrop-blur-md">
      <div className="mx-auto flex h-14 w-full max-w-[1600px] items-center justify-between gap-4 px-4 sm:px-6">
        <Link to="/" className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Calendar className="h-4 w-4" />
          </div>
          <span className="text-sm font-semibold tracking-tight">Clockwise-like</span>
        </Link>

        {weekLabel && (
          <div className="hidden items-center gap-1 rounded-lg border border-border bg-background px-1 py-1 sm:flex">
            <button
              onClick={onPrevWeek}
              className="rounded-md p-1.5 text-muted-foreground transition hover:bg-muted hover:text-foreground"
              aria-label="Previous week"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            <button
              onClick={onToday}
              className="rounded-md px-3 py-1 text-xs font-medium text-foreground transition hover:bg-muted"
            >
              Today
            </button>
            <span className="px-2 text-xs font-medium text-muted-foreground">{weekLabel}</span>
            <button
              onClick={onNextWeek}
              className="rounded-md p-1.5 text-muted-foreground transition hover:bg-muted hover:text-foreground"
              aria-label="Next week"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        )}

        {showSettingsLink && (
          <Link
            to="/settings"
            className="flex items-center gap-2 rounded-lg px-3 py-1.5 text-sm font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground"
          >
            <SettingsIcon className="h-4 w-4" />
            <span className="hidden sm:inline">Settings</span>
          </Link>
        )}
      </div>
    </header>
  );
}
