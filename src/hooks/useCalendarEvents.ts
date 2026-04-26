import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";

export function useCalendarEvents(start: string, end: string) {
  return useQuery({
    queryKey: ["events", start, end],
    queryFn: async () => {
      const data = await api.getEvents(start, end);
      return Array.isArray(data) ? data : [];
    },
    refetchInterval: 5 * 60_000,
    staleTime: 30_000,
  });
}
