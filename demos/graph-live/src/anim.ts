// The frame loop: cools heat, keeps running nodes pulsing and feeding
// particles down their incoming edges, plays the completion wave, and paints
// it all straight onto elements the node and edge components registered.
import { css, edgeId, thermal, type Live } from "./live";

export const nodeEls = new Map<string, HTMLElement>();
export const edgeEls = new Map<string, { path: SVGPathElement; g: SVGGElement }>();

const COOL = 2.2; // seconds: heat's time constant
const TRAIL = 5;

export function startAnimation(live: Live): () => void {
  let raf = 0;
  let last = performance.now();
  let depth = new Map<string, number>();
  let depthVersion = -1;

  const frame = (now: number) => {
    const dt = Math.min(0.1, (now - last) / 1000);
    last = now;
    const t = now / 1000;
    if (depthVersion !== live.version) {
      depth = live.depths();
      depthVersion = live.version;
    }

    for (const n of live.nodes.values()) {
      let h = (live.heat.get(n.id) ?? 0) * Math.exp(-dt / COOL);
      if (n.look === "running") {
        h = Math.max(h, 0.45 + 0.2 * Math.sin(t * 4 + (live.phase.get(n.id) ?? 0)));
        if (n.prereqs.length && now - (live.lastEmit.get(n.id) ?? 0) > 650) {
          live.lastEmit.set(n.id, now);
          const p = n.prereqs[Math.floor(Math.random() * n.prereqs.length)];
          live.particles.push({ from: p, to: n.id, t: 0, speed: 0.55 + Math.random() * 0.3, heat: 0.7 });
        }
      }
      if (live.finished() && !live.flared.has(n.id) && now - live.finaleAt! > (depth.get(n.id) ?? 0) * 110) {
        live.flared.add(n.id);
        h = 1;
        for (const c of n.children) live.particles.push({ from: n.id, to: c, t: 0, speed: 1.5, heat: 1 });
      }
      live.heat.set(n.id, h);

      const el = nodeEls.get(n.id);
      if (el) {
        const glow = thermal(n.look === "done" ? 0.7 + 0.3 * h : n.look === "retry" ? 0.55 : 0.55 + 0.45 * h);
        el.style.setProperty("--heat", h.toFixed(3));
        el.style.setProperty("--glow", css(glow));
        el.style.setProperty("--glow-soft", css(glow, 0.35 * h));
      }
    }

    // Particles, grouped by edge, drawn as a head with a cooling trail.
    const byEdge = new Map<string, typeof live.particles>();
    live.particles = live.particles.filter((p) => {
      p.t += p.speed * dt;
      if (p.t >= 1) {
        live.heat.set(p.to, Math.max(live.heat.get(p.to) ?? 0, p.heat * 0.6));
        return false;
      }
      const id = edgeId(p.from, p.to);
      if (!edgeEls.has(id)) return false;
      (byEdge.get(id) ?? byEdge.set(id, []).get(id)!).push(p);
      return true;
    });
    for (const [id, { path, g }] of edgeEls) {
      const ps = byEdge.get(id) ?? [];
      const want = ps.length * TRAIL;
      while (g.childNodes.length < want) g.appendChild(document.createElementNS("http://www.w3.org/2000/svg", "circle"));
      while (g.childNodes.length > want) g.removeChild(g.lastChild!);
      if (!want) continue;
      const len = path.getTotalLength();
      ps.forEach((p, i) => {
        for (let k = 0; k < TRAIL; k++) {
          const c = g.childNodes[i * TRAIL + k] as SVGCircleElement;
          const tt = Math.max(0, p.t - k * 0.03);
          const pt = path.getPointAtLength(tt * len);
          const heat = p.heat * (1 - k * 0.17);
          c.setAttribute("cx", pt.x.toFixed(1));
          c.setAttribute("cy", pt.y.toFixed(1));
          c.setAttribute("r", (3.2 - k * 0.5).toFixed(2));
          c.setAttribute("fill", css(thermal(heat), 1 - k * 0.18));
        }
      });
    }
    raf = requestAnimationFrame(frame);
  };
  raf = requestAnimationFrame(frame);
  return () => cancelAnimationFrame(raf);
}
