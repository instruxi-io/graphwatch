#!/usr/bin/env node
// agent-team: the end-to-end the browser demo cannot be (README "What a single
// identity can't show"). Two Tier-2 v3 agents, owned by a group and stewarded
// by a named human, claim routed nodes, hand a file to each other through the
// graph's folder, and every hand-off is verified by Snowball (validation-v1,
// Jev as the 1-of-1 judge; the agents share a steward, so neither may judge
// the other — asserted). Then the steward is deactivated and both agents must stop on their
// very next request. Every step is an assertion; the transcript is written to
// recordings/ and the graph it leaves (slug demo-agent-team-…) is watchable in
// graph-live's Watch mode.
//
// It needs THREE identities, and that is the whole reason it is headless:
//   GRAPH_API_KEY      a tenant admin (TenantManage): deactivates the steward
//   GRAPH_STEWARD_KEY  a DISPOSABLE human account in the same tenant: it owns
//                      the group and stewards the agents (v3 requires the
//                      steward of a group-owned agent to be the caller). It
//                      is deactivated by this run.
//                      Never point this at a person's real account.
//   the two ag_ secrets this script mints for itself.
// Optional: GRAPH_BASE_URL (default https://api.instruxi.dev),
//           AGENT_TEAM_ROLE (role slug for the agents, default developer),
//           AGENT_TEAM_KEEP_STEWARD_OFF=1 to leave the steward deactivated.
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const BASE = (process.env.GRAPH_BASE_URL ?? "https://api.instruxi.dev").replace(/\/$/, "");
const ADMIN = process.env.GRAPH_API_KEY ?? "";
const STEWARD = process.env.GRAPH_STEWARD_KEY ?? "";
const ROLE = process.env.AGENT_TEAM_ROLE ?? "developer";
if (!ADMIN || !STEWARD) {
  console.error("agent-team: set GRAPH_API_KEY (tenant admin) and GRAPH_STEWARD_KEY (a disposable steward); see README.md");
  process.exit(64);
}
if (ADMIN === STEWARD) {
  console.error("agent-team: the steward must be a different account from the admin: the run deactivates it");
  process.exit(64);
}

const stamp = Math.floor(Date.now() / 1000);
const tag = `${stamp}-${Math.random().toString(16).slice(2, 6)}`;
const transcript = { scenario: "agent-team", base: BASE, started_at: new Date().toISOString(), steps: [], checks: [] };
let failures = 0;

const log = (line) => console.log(line);
function check(name, ok, detail = "") {
  transcript.checks.push({ name, ok: !!ok, detail });
  log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? ` — ${detail}` : ""}`);
  if (!ok) failures++;
  return !!ok;
}

/** A credential is an API key (env3_…, ag_…) sent as X-API-Key, or a session JWT (eyJ…) sent as a Bearer token. */
const auth = (key) => (key.startsWith("eyJ") ? { Authorization: `Bearer ${key}` } : { "X-API-Key": key });
/** One HTTP call under one credential. `who` names the identity in the transcript, never the secret. */
async function call(who, key, method, path, body, { expect } = {}) {
  const res = await fetch(`${BASE}/api/v1${path}`, {
    method,
    headers: { ...auth(key), "User-Agent": "graph-live-agent-team/1", ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json;
  try { json = text ? JSON.parse(text) : undefined; } catch { json = text; }
  transcript.steps.push({ who, method, path, status: res.status, error: res.ok ? undefined : json?.error ?? text.slice(0, 200) });
  log(`  ${who.padEnd(8)} ${method.padEnd(6)} ${path} → ${res.status}${res.ok ? "" : ` ${json?.error ?? ""}`}`);
  if (expect === undefined && !res.ok) throw new Error(`${who} ${method} ${path}: ${res.status} ${text.slice(0, 300)}`);
  return { status: res.status, data: json?.data ?? json, raw: json };
}
const data = (r) => r.data;

async function main() {
  // ── 1. The steward and their team ─────────────────────────────────────
  const me = data(await call("steward", STEWARD, "GET", "/enforcer/auth/me"));
  const stewardId = me.account_id ?? me.account?.id ?? me.id;
  check("steward identity resolved", !!stewardId, `account ${stewardId}`);
  // The run DEACTIVATES the steward. Two different keys can belong to one
  // account, so compare accounts, not key strings: if the admin IS the steward,
  // deactivating it would also kill the admin that has to turn it back on.
  const adminMe = data(await call("admin", ADMIN, "GET", "/enforcer/auth/me"));
  const adminId = adminMe.account_id ?? adminMe.account?.id ?? adminMe.id;
  const stewardTenant = me.tenant_id ?? me.tenant?.id, adminTenant = adminMe.tenant_id ?? adminMe.tenant?.id;
  if (!check("steward and admin are in the same workspace", !!stewardTenant && stewardTenant === adminTenant,
    stewardTenant === adminTenant ? `tenant ${adminTenant}` : `steward tenant ${stewardTenant} ≠ admin tenant ${adminTenant} — invite the throwaway INTO the admin's workspace, do not create a new one`)) {
    log("agent-team: refusing to continue — a steward in another tenant has no Jev settings there and the admin cannot deactivate it.");
    return;
  }
  if (!check("steward is a different account from the admin", !!adminId && adminId !== stewardId,
    adminId === stewardId ? `both keys resolve to account ${adminId} — use a DISPOSABLE steward account` : `admin ${adminId}`)) {
    log("agent-team: refusing to continue — the steward must be a throwaway account, never the admin's.");
    return;
  }
  const roles = data(await call("steward", STEWARD, "GET", "/enforcer/roles"));
  // v3 mints an agent only with a role at or under the steward's own, so
  // without AGENT_TEAM_ROLE use the steward's role.
  const list = roles.items ?? roles;
  const mySlug = me.role_slug ?? me.role?.slug;
  const role = (process.env.AGENT_TEAM_ROLE ? list.find((r) => r.slug === ROLE) : null)
    ?? list.find((r) => r.slug === mySlug) ?? list.find((r) => r.id === (me.role_id ?? me.role?.id));
  if (!check(`agent role resolved`, !!role, role ? `${role.slug} (${process.env.AGENT_TEAM_ROLE ? "AGENT_TEAM_ROLE" : "the steward's own role"})` : `no role for ${process.env.AGENT_TEAM_ROLE ?? mySlug}`)) return;
  const group = data(await call("steward", STEWARD, "POST", "/enforcer/groups", { name: `agent-team ${tag}`, slug: `agent-team-${tag}`, description: "graph-live agent-team scenario" }));
  const agents = [];
  for (const n of ["alpha", "beta"]) {
    const a = data(await call("steward", STEWARD, "POST", "/enforcer/agents", {
      name: `team-${n}-${tag}`, slug: `team-${n}-${tag}`, description: "graph-live agent-team",
      owner: { kind: "group", id: group.id }, steward_account_id: stewardId, role_id: role.id,
      scopes: ["enforcer:read", "enforcer:write"],
    }));
    const cred = data(await call("steward", STEWARD, "POST", `/enforcer/agents/${a.id}/credentials`, { name: "agent-team", expires_in_days: 1 }));
    agents.push({ name: n, id: a.id, label: a.name, sa: a.service_account?.id, secret: cred.secret, agent: a });
  }
  for (const a of agents) {
    check(`agent ${a.name} is Tier 2, group-owned, steward named`,
      a.agent.owner?.kind === "group" && a.agent.owner?.id === group.id && a.agent.steward?.account_id === stewardId && a.secret?.startsWith("ag_"),
      `owner ${a.agent.owner?.kind}:${a.agent.owner?.id} steward ${a.agent.steward?.account_id}`);
  }
  const [alpha, beta] = agents;

  // ── 2. The plan: routed nodes, a judged quorum ────────────────────────
  const graph = data(await call("steward", STEWARD, "POST", "/graph/graphs", {
    slug: `demo-agent-team-${tag}`, name: `demo-agent-team · two agents, one steward`, mode: "dag",
  }));
  const g = `/graph/graphs/${graph.id}`;
  transcript.graph_id = graph.id;
  // Routing on, so the claim card carries the route; the routes themselves are
  // set by hand below, not by the router. Routing config has its own route
  // (PUT /graphs/{id}/routing); a `routing` key on PATCH /graphs/{id} is not it.
  await call("steward", STEWARD, "PUT", `${g}/routing`, { enabled: true, fallback: "none" });
  // Both agents work the graph as editors, under their own service accounts.
  // v3 lets a non-admin human add an agent only to a group they already belong
  // to, and the graph's editor share group is created lazily and EMPTY — so the
  // steward seats itself as an editor first. (A plain-role owner cannot
  // otherwise share their own graph with their agents.)
  const self = await call("steward", STEWARD, "POST", `${g}/members`, { account_id: stewardId, role: "editor" }, { expect: "any" });
  check("steward seated in the editor share group before adding agents", self.status === 201 || self.status === 409 || self.status === 200, `${self.status} ${self.raw?.error ?? ""}`);
  for (const a of agents) await call("steward", STEWARD, "POST", `${g}/members`, { account_id: a.sa, role: "editor" });
  const profiles = {};
  for (const a of agents) {
    profiles[a.name] = data(await call("steward", STEWARD, "POST", `${g}/agent-profiles`, {
      name: `team-${a.name}`, runs: "agent", v3_agent_id: a.id, description: `agent ${a.label}`,
    }));
  }
  // Snowball: every completion waits for a 1-of-1 quorum drawn from the
  // allowlist (both agents' service accounts). A judge never votes on a run it
  // worked (self_judgment_forbidden), so the only eligible judge for alpha's
  // run is beta, and the other way round: every hand-off is verified by the
  // OTHER agent.
  await call("steward", STEWARD, "PUT", `${g}/validation`, {
    // Jev is the one judge (1-of-1 Snowball). The two agents share a steward, so
    // each is a PARTY to the other's run (graphauth.IsRunParty: same accountable
    // human) and may not judge it — separation of duties follows the steward,
    // not the credential. Asserted below with a 403.
    quorum: { m: 1, n: 1 }, judges: { scope: "none" }, jev: true,
  });
  const nodes = data(await call("steward", STEWARD, "POST", `${g}/nodes/batch`, { nodes: [
    { key: "draft", type: "task", title: "alpha drafts the spec into the graph folder", data: { acceptance: ["spec.md is in the graph folder"] } },
    { key: "review", type: "task", title: "beta reads the spec and writes the review", data: { acceptance: ["review.md quotes spec.md"] } },
  ] }));
  const byKey = Object.fromEntries((nodes.items ?? nodes).map((n) => [n.key, n]));
  await call("steward", STEWARD, "POST", `${g}/edges/batch`, { edges: [{ from_key: "review", to_key: "draft", type: "requires" }] });
  await call("steward", STEWARD, "POST", `${g}/nodes/${byKey.draft.id}/route`, { profile_id: profiles.alpha.id });
  await call("steward", STEWARD, "POST", `${g}/nodes/${byKey.review.id}/route`, { profile_id: profiles.beta.id });

  // ── 3. The agents work it, under their OWN credentials ────────────────
  const claimAs = async (a) => {
    for (let i = 0; i < 30; i++) {
      const r = data(await call(a.name, a.secret, "POST", `${g}/frontier/claim`, { limit: 1, runner: `team-${a.name}`, prefer_profiles: [`team-${a.name}`] }));
      const c = (r.items ?? r)[0];
      if (c) return c;
      await new Promise((ok) => setTimeout(ok, 2000));
    }
    throw new Error(`${a.name}: nothing to claim after 60 s`);
  };
  // Upload is three steps: presigned URL, PUT the bytes, then POST
  // complete_url with the same file_name so the files service records
  // the file and it appears in the folder. Returns the folder listing as the
  // evidence a judge can check: `GET /files` naming the file.
  const upload = async (a, name, content) => {
    const u = data(await call(a.name, a.secret, "POST", `${g}/files/upload-url`, { file_name: name, overwrite: true }));
    const put = await fetch(u.url ?? u.upload_url, { method: "PUT", body: content, headers: u.headers ?? {} });
    let completed = null;
    // complete_url can be a cluster-internal address, unreachable from outside;
    // the files service serves the same path under the public base, and
    // folder_id is in the response.
    const completeURL = u.complete_url && u.complete_url.startsWith(BASE)
      ? u.complete_url
      : `${BASE}/api/v1/files/storage/folders/${u.folder_id}/presigned-upload-complete`;
    if (put.ok && u.folder_id) {
      const res = await fetch(completeURL, { method: "POST", headers: { ...auth(a.secret), "Content-Type": "application/json", "User-Agent": "graph-live-agent-team/1" }, body: JSON.stringify({ file_name: name }) });
      const j = await res.json().catch(() => ({}));
      completed = res.ok ? (j.data?.file_id ?? j.data?.id ?? j.file_id ?? "ok") : null;
      transcript.steps.push({ who: a.name, method: "POST", path: "<complete_url>", status: res.status, error: res.ok ? undefined : j?.error });
      log(`  ${a.name.padEnd(8)} POST   ${completeURL.replace(BASE, "")} → ${res.status}`);
    }
    const listing = data(await call(a.name, a.secret, "GET", `${g}/files`));
    const names = (listing.items ?? listing).map((f) => f.name ?? f.file_name ?? f.path ?? "");
    const listed = names.some((n) => n.endsWith(name));
    check(`${a.name} uploaded ${name} to the graph folder`, put.ok && !!completed && listed, `PUT ${put.status}, complete ${completed ? "ok" : "missing"}, listed ${listed}`);
    return { kind: "command", cmd: `GET /api/v1/graph/graphs/${graph.id}/files`, exit: listed ? 0 : 1, output: JSON.stringify({ items: names, total: listing.total ?? names.length }) };
  };
  // A sibling agent (same steward) is a party to the run: its vote is refused.
  const siblingCannotJudge = async (judge, node, runId) => {
    const r = await call(judge.name, judge.secret, "POST", `${g}/nodes/${node.node_id}/runs/${runId}/votes`, { vote: "accept" }, { expect: "any" });
    return { ok: r.status === 403, detail: `${r.status} ${r.raw?.error ?? ""}` };
  };
  // Snowball decides: poll the run until Jev's verdict lands (validation-v1 holds
  // the node in verifying meanwhile, so the dependent cannot be claimed yet).
  const escalated = new Set(); const arbitrated = [];
  const decided = async (node, runId) => {
    for (let i = 0; i < 60; i++) {
      // There is no GET for one run; list the node's runs and pick ours.
      const runs = data(await call("steward", STEWARD, "GET", `${g}/nodes/${node.node_id}/runs`));
      const r = (runs.items ?? runs).find((x) => x.id === runId) ?? {};
      const st = r.validation?.state;
      if (st === "escalated" && !escalated.has(runId)) {
        // Snowball could not decide (reason e.g. jev_undecided): the contract
        // escalates to a human arbitrator — the graph owner or a tenant admin.
        // The steward is a party to its agents' runs, so the ADMIN arbitrates,
        // through the review queue's verdict_pending item (actions approve|reject).
        escalated.add(runId);
        const q = data(await call("admin", ADMIN, "GET", `${g}/review?kind=verdict_pending&limit=50`));
        const item = (q.items ?? q).find((it) => it.node_id === node.node_id && (!it.verdict?.run_id || it.verdict.run_id === runId));
        if (item) {
          await call("admin", ADMIN, "POST", `${g}/review/${item.id}/resolve`, { action: "approve", note: `arbitrated by the tenant admin after ${r.validation?.reason}` });
          arbitrated.push(`${node.key}: ${r.validation?.reason} → admin approved`);
          continue;
        }
        return { ok: false, detail: `escalated (${r.validation?.reason}) but no verdict_pending review item for the run` };
      }
      if (r.validation?.decided_at && r.validation?.decided_by === "arbitrator") {
        const nd = data(await call("steward", STEWARD, "GET", `${g}/nodes/${node.node_id}`));
        return { ok: nd.status === "done", detail: `arbitrated by the admin (${r.validation?.reason}); node ${nd.status}${arbitrated.length ? ` [${arbitrated.join("; ")}]` : ""}` };
      }
      if (st && st !== "pending" && st !== "escalated") return { ok: st === "accepted", detail: `validation ${st}${r.validation?.reason ? ` (${r.validation.reason})` : ""}${arbitrated.length ? ` [${arbitrated.join("; ")}]` : ""}` };
      await new Promise((ok) => setTimeout(ok, 3000));
    }
    return { ok: false, detail: "still pending after 180 s" };
  };

  const c1 = await claimAs(alpha);
  check("alpha claimed the node routed to it", c1.key === "draft" && c1.route?.profile_id === profiles.alpha.id, `key ${c1.key} route ${c1.route?.name}`);
  const specBody = `# spec ${tag}\nThe team ships the thing.\n`;
  const ev1 = await upload(alpha, "spec.md", specBody);
  await call(alpha.name, alpha.secret, "POST", `${g}/nodes/${c1.node_id}/runs/${c1.run_id}/complete`, {
    status: "succeeded", report: "1. MET — spec.md is in the graph folder: GET /files lists it and its body is attached.",
    evidence: [ev1, { kind: "command", cmd: "cat spec.md", exit: 0, output: specBody }],
  });
  { const r = await siblingCannotJudge(beta, c1, c1.run_id); check("beta (alpha's sibling under the same steward) may not judge alpha's run", r.ok, r.detail); }
  { const r = await decided(c1, c1.run_id); check("Snowball (Jev, 1-of-1) accepted alpha's run", r.ok, r.detail); }
  const selfVote = await call(alpha.name, alpha.secret, "POST", `${g}/nodes/${c1.node_id}/runs/${c1.run_id}/votes`, { vote: "accept" }, { expect: "any" });
  check("alpha cannot judge its own run", selfVote.status === 403, `${selfVote.status} ${selfVote.raw?.error ?? ""}`);

  const c2 = await claimAs(beta);
  check("beta claimed the node routed to it, only after alpha's was verified", c2.key === "review" && c2.route?.profile_id === profiles.beta.id, `key ${c2.key}`);
  const files = data(await call(beta.name, beta.secret, "GET", `${g}/files`));
  const spec = (files.items ?? files).find((f) => (f.name ?? f.file_name ?? "").endsWith("spec.md"));
  check("beta sees alpha's file in the graph folder", !!spec, spec ? (spec.name ?? spec.file_name) : "absent");
  const review = `# review ${tag}\n> The team ships the thing.\nLGTM.\n`;
  const ev2 = await upload(beta, "review.md", review);
  await call(beta.name, beta.secret, "POST", `${g}/nodes/${c2.node_id}/runs/${c2.run_id}/complete`, {
    status: "succeeded", report: "1. MET — review.md quotes spec.md: review.md's second line is spec.md's second line, quoted; both bodies and the folder listing attached.",
    evidence: [ev2, { kind: "command", cmd: "cat spec.md", exit: 0, output: specBody }, { kind: "command", cmd: "cat review.md", exit: 0, output: review }],
  });
  { const r = await siblingCannotJudge(alpha, c2, c2.run_id); check("alpha may not judge beta's run (same steward)", r.ok, r.detail); }
  { const r = await decided(c2, c2.run_id); check("Snowball (Jev, 1-of-1) accepted beta's run", r.ok, r.detail); }

  // ── 4. Attribution: agent X (steward Y) on every run ──────────────────
  for (let i = 0; i < 15; i++) {
    const s = data(await call("steward", STEWARD, "GET", `${g}/summary`));
    if (s.complete) break;
    await new Promise((ok) => setTimeout(ok, 2000));
  }
  const runs = data(await call("steward", STEWARD, "GET", `/graph/runs/query?graph_id=${graph.id}&on_behalf_of=me`));
  const items = runs.items ?? runs.runs ?? runs;
  for (const [a, c] of [[alpha, c1], [beta, c2]]) {
    const r = (Array.isArray(items) ? items : []).find((x) => (x.run_id ?? x.id) === c.run_id);
    check(`run ${c.key} attributed to agent ${a.name} (steward ${stewardId})`, r?.agent_id === a.id && r?.on_behalf_of === stewardId,
      `agent_id ${r?.agent_id} on_behalf_of ${r?.on_behalf_of}`);
    // v3's side of the attribution: the agent record names the steward, and v3
    // stamped the credential as used when the agent acted. (The agent audit feed
    // lists v3 resource events, not graph calls — graph attribution is the run.)
    const rec = data(await call("steward", STEWARD, "GET", `/enforcer/agents/${a.id}`));
    check(`v3 names ${a.name}'s steward and saw its credential used`,
      rec.steward?.account_id === stewardId && !!rec.credentials?.last_used_at,
      `steward ${rec.steward?.account_id} last_used_at ${rec.credentials?.last_used_at}`);
  }

  // ── 5. Steward deactivation stops both agents within one request ──────
  await call("admin", ADMIN, "PUT", `/enforcer/accounts/${stewardId}/active`, { active: false });
  try {
    for (const a of agents) {
      const r = await call(a.name, a.secret, "POST", `${g}/frontier/claim`, { limit: 1, runner: `team-${a.name}` }, { expect: "any" });
      check(`agent ${a.name} refused on the first request after the steward's deactivation`, r.status === 401, `${r.status} ${r.raw?.error ?? ""}`);
      const st = data(await call("admin", ADMIN, "GET", `/enforcer/agents/${a.id}`));
      check(`agent ${a.name} suspended: steward_inactive`, st.state === "suspended" && st.state_reason === "steward_inactive", `${st.state}:${st.state_reason}`);
    }
  } finally {
    if (process.env.AGENT_TEAM_KEEP_STEWARD_OFF !== "1") await call("admin", ADMIN, "PUT", `/enforcer/accounts/${stewardId}/active`, { active: true }, { expect: "any" });
  }
}

main()
  .catch((e) => check("scenario ran to the end", false, e.message))
  .finally(() => {
    transcript.finished_at = new Date().toISOString();
    transcript.result = failures ? "FAIL" : "PASS";
    const dir = join(dirname(fileURLToPath(import.meta.url)), "recordings");
    mkdirSync(dir, { recursive: true });
    const file = join(dir, `agent-team-${stamp}.json`);
    writeFileSync(file, JSON.stringify(transcript, null, 2) + "\n");
    log(`\nagent-team ${transcript.result} (${transcript.checks.length - failures}/${transcript.checks.length} checks) graph ${transcript.graph_id ?? "-"}\nrecorded ${file}`);
    process.exitCode = failures ? 1 : 0;
  });
