import { BaseEdge, getBezierPath, type EdgeProps } from "@xyflow/react";
import { memo, useCallback, useRef } from "react";
import { edgeEls } from "./anim";

export type FlowData = { state: "feeding" | "settled" | "released" | "idle"; linked?: boolean };

// An edge's colour says what it carries; the particles on it are painted by
// anim.ts into the <g> registered here, in the same coordinate space as the path.
export const FlowEdge = memo(function FlowEdge(p: EdgeProps & { data: FlowData }) {
  const [d] = getBezierPath({ ...p, curvature: 0.35 });
  const path = useRef<SVGPathElement | null>(null);
  const g = useCallback(
    (el: SVGGElement | null) => {
      if (el && path.current) edgeEls.set(p.id, { path: path.current, g: el });
      else edgeEls.delete(p.id);
    },
    [p.id],
  );
  return (
    <>
      <BaseEdge path={d} className={`flow ${p.data.state}${p.data.linked ? " linked" : ""}`} interactionWidth={14} />
      <path ref={path} d={d} fill="none" stroke="none" />
      <g ref={g} className="particles" />
    </>
  );
});
