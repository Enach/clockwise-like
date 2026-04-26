import { useCallback, useEffect, useState } from "react";

export type ToastVariant = "success" | "error" | "info";

export interface ToastItem {
  id: number;
  message: string;
  variant: ToastVariant;
}

type Listener = (toasts: ToastItem[]) => void;

let toasts: ToastItem[] = [];
let nextId = 1;
const listeners = new Set<Listener>();

function emit() {
  listeners.forEach((l) => l(toasts));
}

function push(message: string, variant: ToastVariant) {
  const id = nextId++;
  toasts = [...toasts, { id, message, variant }];
  emit();
  setTimeout(() => dismiss(id), 4000);
  return id;
}

function dismiss(id: number) {
  toasts = toasts.filter((t) => t.id !== id);
  emit();
}

export const toast = {
  success: (m: string) => push(m, "success"),
  error: (m: string) => push(m, "error"),
  info: (m: string) => push(m, "info"),
};

export function useToast() {
  const [items, setItems] = useState<ToastItem[]>(toasts);
  useEffect(() => {
    const l: Listener = (t) => setItems(t);
    listeners.add(l);
    return () => {
      listeners.delete(l);
    };
  }, []);
  const remove = useCallback((id: number) => dismiss(id), []);
  return { toasts: items, dismiss: remove, toast };
}
