import { useToast } from "@/hooks/useToast";
import { CheckCircle2, AlertCircle, Info, X } from "lucide-react";

const variantStyles: Record<string, string> = {
  success: "bg-success text-success-foreground",
  error: "bg-destructive text-destructive-foreground",
  info: "bg-info text-info-foreground",
};

const VariantIcon = ({ v }: { v: string }) => {
  if (v === "success") return <CheckCircle2 className="h-5 w-5 shrink-0" />;
  if (v === "error") return <AlertCircle className="h-5 w-5 shrink-0" />;
  return <Info className="h-5 w-5 shrink-0" />;
};

export function ToastViewport() {
  const { toasts, dismiss } = useToast();
  return (
    <div className="pointer-events-none fixed inset-0 z-[100] flex flex-col items-end justify-end gap-2 p-4 sm:p-6">
      {toasts.map((t) => (
        <div
          key={t.id}
          role="status"
          className={`pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-xl px-4 py-3 shadow-lg ring-1 ring-black/5 animate-slide-in-right ${variantStyles[t.variant]}`}
        >
          <VariantIcon v={t.variant} />
          <p className="flex-1 text-sm font-medium leading-snug">{t.message}</p>
          <button
            onClick={() => dismiss(t.id)}
            className="rounded-md p-0.5 opacity-80 transition hover:bg-black/10 hover:opacity-100"
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      ))}
    </div>
  );
}
