import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";

export function useFocusBlocks(weekISO: string) {
  return useQuery({
    queryKey: ["focusBlocks", weekISO],
    queryFn: async () => {
      const data = await api.getFocusBlocks(weekISO);
      return Array.isArray(data) ? data : [];
    },
    staleTime: 30_000,
  });
}
