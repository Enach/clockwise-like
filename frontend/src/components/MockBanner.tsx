import { useEffect, useState } from "react";
import { subscribeMockMode } from "@/api/client";
import { Wifi } from "lucide-react";

export function MockBanner() {
  const [on, setOn] = useState(false);
  useEffect(() => subscribeMockMode(setOn), []);
  if (!on) return null;
  return (
    <div className="border-b border-warning/30 bg-warning/10 px-4 py-1.5 text-center text-[11px] font-medium text-warning">
      <Wifi className="mr-1 inline h-3 w-3" />
      Backend not reachable — showing demo data. Connect your Go backend at <code className="font-mono">/api</code> to use real data.
    </div>
  );
}
