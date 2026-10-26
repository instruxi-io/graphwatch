import { Handle, Position, type NodeProps } from "@xyflow/react";
import { memo, useCallback } from "react";
import { nodeEls } from "./anim";
import type { Look } from "./live";

export type DotData = { key: string; look: Look; selected?: boolean };

// A node is a dot whose shading is its work state (the server's, see
// live.ts lookOf). The glow (--glow, --heat) is set every frame by anim.ts,
// not by React.
export const DotNode = memo(function DotNode({ id, data }: NodeProps & { data: DotData }) {
  const ref = useCallback(
    (el: HTMLDivElement | null) => {
      if (el) nodeEls.set(id, el);
      else nodeEls.delete(id);
    },
    [id],
  );
  return (
    <div className={`dot ${data.look}${data.selected ? " selected" : ""}`} ref={ref}>
      <Handle type="target" position={Position.Top} isConnectable={false} />
      <div className="core" />
      <div className="label">{data.key}</div>
      <Handle type="source" position={Position.Bottom} isConnectable={false} />
    </div>
  );
});
