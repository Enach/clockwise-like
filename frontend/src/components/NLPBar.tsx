import { useState } from "react";
import { Send, Loader2, Sparkles } from "lucide-react";

interface NLPBarProps {
  initialValue?: string;
  loading?: boolean;
  error?: string | null;
  onSubmit: (text: string) => void;
}

export function NLPBar({ initialValue, loading, error, onSubmit }: NLPBarProps) {
  const [text, setText] = useState(initialValue ?? "");

  return (
    <div className="space-y-2">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!loading && text.trim()) onSubmit(text.trim());
        }}
        className="group flex items-center gap-2 rounded-2xl border border-border bg-card px-3 py-2 shadow-sm transition focus-within:border-primary focus-within:ring-2 focus-within:ring-primary/20"
      >
        <Sparkles className="h-4 w-4 shrink-0 text-primary" />
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Schedule a meeting… e.g. '30 min with alice@co.com this week'"
          className="flex-1 bg-transparent text-sm text-foreground placeholder:text-muted-foreground focus:outline-none"
          disabled={loading}
        />
        <button
          type="submit"
          disabled={loading || !text.trim()}
          className="flex items-center gap-1.5 rounded-lg bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground shadow-sm transition hover:bg-primary/90 disabled:opacity-50"
        >
          {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
          <span>{loading ? "Thinking…" : "Send"}</span>
        </button>
      </form>
      {error && (
        <p className="px-3 text-xs font-medium text-destructive">{error}</p>
      )}
    </div>
  );
}
