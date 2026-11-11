"use strict";

const TOKEN = document.querySelector('meta[name="beechief-token"]').content;
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  meta: null,
  inventory: null,
  selected: null,
  config: null,
  jobs: [],
  swarm: null,
  swarmTimer: null,
  editJob: null,
  editHive: null,
};

function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

function mk(tag, cls, text) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  if (text !== undefined && text !== null) el.textContent = String(text);
  return el;
}

function banner(message, kind = "info") {
  const el = $("#banner");
  el.textContent = message || "";
  el.className = "banner" + (kind === "info" ? "" : " " + kind);
  el.classList.toggle("hidden", !message);
}

async function api(path, options = {}) {
  const init = {
    method: options.method || "GET",
    headers: { "X-BeeChief-Token": TOKEN },
  };
  if (options.body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(options.body);
  }
  const res = await fetch(path, init);
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    const msg = data && typeof data === "object" && data.error ? data.error : (res.statusText || "request failed");
    throw new Error(msg);
  }
  return data;
}

function fmtTime(value) {
  if (!value || value.startsWith("0001-")) return "never";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "never";
  return d.toLocaleString();
}

function pretty(value) {
  return typeof value === "string" ? value : JSON.stringify(value, null, 2);
}

function setBusy(busy) {
  ["#scan-btn", "#report-btn"].forEach((sel) => {
    const el = $(sel);
    if (el) el.disabled = busy;
  });
}

function setTab(name) {
  $$("#tabs button").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
  $$(".tab").forEach((s) => s.classList.toggle("active", s.id === "tab-" + name));
  if (name === "jobs") loadJobs().catch((e) => banner(e.message, "error"));
  if (name === "settings") loadConfig().catch((e) => banner(e.message, "error"));
  populateHiveSelects();
}

function hiveNames() {
  if (state.inventory && state.inventory.hives.length) {
    return state.inventory.hives.map((h) => h.name);
  }
  if (state.config && state.config.hives) {
    return state.config.hives.map((h) => h.name);
  }
  return [];
}

function fillSelect(select, names, includeAll) {
  if (!select) return;
  const current = select.value;
  clear(select);
  if (includeAll) select.appendChild(new Option("All Hives", ""));
  names.forEach((name) => select.appendChild(new Option(name, name)));
  if (names.includes(current) || (includeAll && current === "")) select.value = current;
}

function populateHiveSelects() {
  const names = hiveNames();
  fillSelect($("#swarm-hive"), names, false);
  fillSelect($("#hive-select"), names, false);
  fillSelect($("#log-hive"), names, false);
}

async function scan() {
  banner("Scanning Hives...");
  setBusy(true);
  try {
    state.inventory = await api("/api/scan", { method: "POST" });
    renderAll();
    banner(`Scan complete: ${state.inventory.total} Bee(s), ${state.inventory.failed} Hive(s) failed.`, "ok");
  } catch (e) {
    banner(e.message, "error");
  } finally {
    setBusy(false);
  }
}

function renderAll() {
  renderSwarm();
  populateHiveSelects();
  renderBee();
}

function renderSwarm() {
  const cards = $("#hive-cards");
  clear(cards);
  const inv = state.inventory;
  if (!inv) {
    cards.appendChild(mk("div", "card", "No scan yet. Press Rescan."));
    $("#swarm-summary").textContent = "";
    return;
  }
  $("#swarm-summary").textContent = `${inv.total} Bee(s) across ${inv.hives.length} Hive(s), ${inv.failed} failed`;
  if (!inv.hives.length) {
    cards.appendChild(mk("div", "card", "No Hives configured. Add one in Settings."));
    return;
  }
  inv.hives.forEach((hive) => {
    const card = mk("div", "card");
    const head = mk("div", "card-head");
    const title = mk("div", "card-title");
    title.appendChild(mk("span", "dot " + (hive.ok ? "ok" : "fail")));
    title.appendChild(document.createTextNode(hive.name));
    head.appendChild(title);
    head.appendChild(mk("span", "badge " + (hive.ok ? "ok" : "fail"), hive.ok ? `${(hive.bees || []).length} bees` : "offline"));
    card.appendChild(head);
    if (!hive.ok && hive.error) {
      card.appendChild(mk("div", "muted", hive.error));
    }
    const list = mk("div", "bee-list");
    (hive.bees || []).forEach((bee) => {
      const row = mk("div", "bee-row");
      if (state.selected && state.selected.hive === hive.name && state.selected.id === bee.id) {
        row.classList.add("selected");
      }
      const name = mk("div");
      name.appendChild(mk("strong", null, bee.name || bee.id));
      name.appendChild(mk("span", "muted", "  " + bee.id));
      row.appendChild(name);
      const badges = mk("div");
      if (bee.default) badges.appendChild(mk("span", "badge info", "default"));
      if (bee.runtime) badges.appendChild(mk("span", "badge", bee.runtime));
      row.appendChild(badges);
      row.addEventListener("click", () => selectBee(hive.name, bee.id));
      list.appendChild(row);
    });
    if (!(hive.bees || []).length) list.appendChild(mk("div", "muted", "No Bees discovered."));
    card.appendChild(list);
    cards.appendChild(card);
  });
}

function selectBee(hive, id) {
  state.selected = { hive, id };
  setTab("bee");
  renderBee();
  renderSwarm();
}

function selectedBee() {
  if (!state.selected || !state.inventory) return null;
  for (const hive of state.inventory.hives) {
    if (hive.name !== state.selected.hive) continue;
    return hive.bees.find((b) => b.id === state.selected.id) || null;
  }
  return null;
}

function renderBee() {
  const bee = selectedBee();
  const actions = $("#bee-actions");
  clear(actions);
  clear($("#bee-extra"));
  $("#bee-output").textContent = "";
  if (!bee) {
    $("#bee-title").textContent = "Bee";
    $("#bee-subtitle").textContent = "Select a Bee from the Swarm tab.";
    return;
  }
  $("#bee-title").textContent = bee.name || bee.id;
  $("#bee-subtitle").textContent = `${bee.hive}/${bee.id}`;

  const details = mk("div", "muted",
    `runtime=${bee.runtime || "default"}  workspace=${bee.workspace || "n/a"}  status=${bee.status || "ready"}${bee.default ? "  default" : ""}`);
  $("#bee-extra").appendChild(details);

  const add = (label, fn, cls = "secondary") => {
    const b = mk("button", cls, label);
    b.addEventListener("click", fn);
    actions.appendChild(b);
  };
  add("Skills", () => beeGet("skills"), "primary");
  add("Skill check", () => beeGet("check"));
  add("Self-review", () => beePost("self-review"));
  add("Workshop", () => beeGet("workshop"));
  add("Models", () => beeGet("models"));

  if (!state.meta || !state.meta.readOnly) {
    const extra = $("#bee-extra");
    const panel = mk("div", "panel");
    panel.appendChild(mk("h3", null, "Send instruction"));
    const row = mk("div", "form-row");
    const input = mk("input", "wide");
    input.placeholder = "Instruction for this Bee";
    const send = mk("button", "primary", "Send");
    send.addEventListener("click", () => sendInstruction(input.value));
    row.appendChild(input);
    row.appendChild(send);
    panel.appendChild(row);
    extra.appendChild(panel);
  }
}

async function beeGet(action) {
  const bee = selectedBee();
  if (!bee) return;
  const q = new URLSearchParams({ hive: bee.hive, id: bee.id });
  banner("Loading " + action + "...");
  try {
    const data = await api(`/api/bee/${action}?${q}`);
    renderBeeResult(action, data);
    banner("");
  } catch (e) {
    banner(e.message, "error");
  }
}

async function beePost(action) {
  const bee = selectedBee();
  if (!bee) return;
  const q = new URLSearchParams({ hive: bee.hive, id: bee.id });
  banner("Running " + action + "...");
  try {
    const data = await api(`/api/bee/${action}?${q}`, { method: "POST" });
    renderBeeResult(action, data);
    banner("");
  } catch (e) {
    banner(e.message, "error");
  }
}

async function sendInstruction(message) {
  const bee = selectedBee();
  if (!bee) return;
  if (!message.trim()) {
    banner("Type an instruction first.", "error");
    return;
  }
  banner("Sending instruction...");
  try {
    const data = await api("/api/bee/instruction", {
      method: "POST",
      body: { hive: bee.hive, id: bee.id, message },
    });
    $("#bee-output").textContent = data.reply || "(no reply)";
    banner("Instruction delivered.", "ok");
  } catch (e) {
    banner(e.message, "error");
  }
}

function skillState(skill) {
  const missing = skill.missing || {};
  const hasMissing = ["bins", "anyBins", "env", "config", "os"].some((k) => (missing[k] || []).length);
  if (skill.eligible && !skill.disabled && !skill.blockedByAllowlist && !skill.blockedByAgentFilter && !hasMissing) return ["ready", "ok"];
  if (hasMissing) {
    const parts = [];
    ["bins", "anyBins", "env", "config", "os"].forEach((k) => {
      if ((missing[k] || []).length) parts.push(`${k}: ${missing[k].join(", ")}`);
    });
    return ["missing (" + parts.join("; ") + ")", "warn"];
  }
  if (skill.blockedByAllowlist || skill.blockedByAgentFilter) return ["blocked", "fail"];
  if (skill.disabled) return ["disabled", "warn"];
  return ["ineligible", "warn"];
}

function renderBeeResult(action, data) {
  const out = $("#bee-output");
  clear(out);
  if (action === "skills") {
    const table = mk("table");
    const head = mk("tr");
    ["Skill", "State", "Source"].forEach((h) => head.appendChild(mk("th", null, h)));
    table.appendChild(head);
    (data.skills || []).forEach((skill) => {
      const tr = mk("tr");
      tr.appendChild(mk("td", null, skill.name));
      const [label, kind] = skillState(skill);
      const td = mk("td");
      td.appendChild(mk("span", "badge " + kind, label));
      tr.appendChild(td);
      tr.appendChild(mk("td", "muted", skill.source || ""));
      table.appendChild(tr);
    });
    out.appendChild(table);
    if (!(data.skills || []).length) out.textContent = "No skills reported.";
    return;
  }
  if (action === "check") {
    const s = data.summary || {};
    const summary = mk("div", "muted",
      `total=${s.total ?? 0} eligible=${s.eligible ?? 0} missing=${s.missingRequirements ?? 0} disabled=${s.disabled ?? 0} blocked=${s.blocked ?? 0}`);
    out.appendChild(summary);
    const missing = data.missingRequirements || [];
    if (missing.length) {
      const table = mk("table");
      const head = mk("tr");
      ["Missing requirement", "State"].forEach((h) => head.appendChild(mk("th", null, h)));
      table.appendChild(head);
      missing.forEach((skill) => {
        const tr = mk("tr");
        tr.appendChild(mk("td", null, skill.name));
        const [label, kind] = skillState(skill);
        const td = mk("td");
        td.appendChild(mk("span", "badge " + kind, label));
        tr.appendChild(td);
        table.appendChild(tr);
      });
      out.appendChild(table);
    }
    return;
  }
  if (action === "self-review") {
    out.appendChild(mk("pre", "output", data.summary || "(no summary)"));
    const proposals = data.proposals || data.Proposals || [];
    if (proposals.length) {
      out.appendChild(mk("h3", null, "Workshop proposals"));
      proposals.forEach((p) => out.appendChild(mk("div", null, `${p.name} - ${p.description || ""}`)));
    }
    return;
  }
  if (action === "workshop") {
    const proposals = data.skills || [];
    if (!proposals.length) {
      out.textContent = "No Workshop proposals.";
      return;
    }
    const table = mk("table");
    const head = mk("tr");
    ["Proposal", "Uses", "Updated", ""].forEach((h) => head.appendChild(mk("th", null, h)));
    table.appendChild(head);
    proposals.forEach((p) => {
      const tr = mk("tr");
      tr.appendChild(mk("td", null, p.name));
      tr.appendChild(mk("td", null, p.useCount ?? 0));
      tr.appendChild(mk("td", "muted", fmtTime(p.updatedAtMs ? new Date(p.updatedAtMs).toISOString() : "")));
      const td = mk("td");
      const btn = mk("button", "small secondary", "View");
      btn.addEventListener("click", () => showProposal(p.name));
      td.appendChild(btn);
      tr.appendChild(td);
      table.appendChild(tr);
    });
    out.appendChild(table);
    return;
  }
  if (action === "models") {
    const lines = [];
    lines.push("Default model: " + (data.defaultModel || "none"));
    if (data.allowed && data.allowed.length) lines.push("Allowed: " + data.allowed.join(", "));
    if (data.missingProvidersInUse && data.missingProvidersInUse.length) lines.push("Missing providers: " + data.missingProvidersInUse.join(", "));
    if (data.modelRouteIssues && data.modelRouteIssues.length) lines.push("Route issues: " + data.modelRouteIssues.join("; "));
    out.textContent = lines.join("\n");
    return;
  }
  out.textContent = pretty(data);
}

async function showProposal(name) {
  const bee = selectedBee();
  if (!bee) return;
  const q = new URLSearchParams({ hive: bee.hive, id: bee.id, name });
  try {
    const data = await api(`/api/bee/workshop/proposal?${q}`);
    $("#bee-output").textContent = data.content || pretty(data);
  } catch (e) {
    banner(e.message, "error");
  }
}

function selectedHive(selectId) {
  return $(selectId).value || hiveNames()[0] || "";
}

async function showHealth() {
  const hive = selectedHive("#hive-select");
  if (!hive) return;
  $("#hive-raw").textContent = "";
  banner("Running Hive Health on " + hive + "...");
  try {
    const rep = await api(`/api/hive/health?hive=${encodeURIComponent(hive)}`);
    renderHealth(rep);
    banner("");
  } catch (e) {
    banner(e.message, "error");
  }
}

function renderHealth(rep) {
  const out = $("#hive-output");
  clear(out);
  const counts = { pass: 0, warning: 0, fail: 0, unavailable: 0 };
  (rep.checks || []).forEach((c) => { if (counts[c.state] !== undefined) counts[c.state]++; });
  out.appendChild(mk("div", "muted",
    `pass=${counts.pass} warning=${counts.warning} fail=${counts.fail} unavailable=${counts.unavailable}`));
  const list = mk("div", "check-list");
  (rep.checks || []).forEach((c) => {
    const item = mk("div", "check-item");
    const kind = c.state === "pass" ? "ok" : c.state === "fail" ? "fail" : "warn";
    item.appendChild(mk("span", "badge " + kind, c.state));
    item.appendChild(mk("span", "name", c.name + ": " + c.summary));
    if (c.remediation && c.remediation.length) item.title = c.remediation.join("\n");
    list.appendChild(item);
  });
  out.appendChild(list);
}

async function showServices() {
  const hive = selectedHive("#hive-select");
  if (!hive) return;
  $("#hive-raw").textContent = "";
  banner("Inspecting services on " + hive + "...");
  try {
    const rep = await api(`/api/hive/services?hive=${encodeURIComponent(hive)}`);
    renderServices(hive, rep);
    banner("");
  } catch (e) {
    banner(e.message, "error");
  }
}

function renderServices(hive, rep) {
  const out = $("#hive-output");
  clear(out);
  const sys = rep.systemd || {};
  out.appendChild(mk("h3", null, "systemd"));
  if (!sys.available) {
    out.appendChild(mk("div", "muted", sys.err || "systemd unavailable"));
  } else {
    const table = mk("table");
    const head = mk("tr");
    ["Unit", "Active", "Enabled", ""].forEach((h) => head.appendChild(mk("th", null, h)));
    table.appendChild(head);
    (sys.units || []).forEach((unit) => {
      const tr = mk("tr");
      tr.appendChild(mk("td", null, unit.name));
      tr.appendChild(mk("td", null, unit.activeState || unit.loadState || ""));
      tr.appendChild(mk("td", null, unit.enabledState || ""));
      const td = mk("td");
      const btn = mk("button", "small secondary", "details");
      btn.addEventListener("click", () => showUnit(hive, unit.name));
      td.appendChild(btn);
      if (!state.meta || !state.meta.readOnly) {
        ["start", "stop", "restart"].forEach((action) => {
          const b = mk("button", "small " + (action === "stop" ? "danger" : "secondary"), action);
          b.addEventListener("click", () => controlUnit(hive, unit.name, action));
          td.appendChild(b);
        });
      }
      tr.appendChild(td);
      table.appendChild(tr);
    });
    out.appendChild(table);
    if (!(sys.units || []).length) out.appendChild(mk("div", "muted", "No units reported."));
  }

  const docker = rep.docker || {};
  out.appendChild(mk("h3", null, "Docker"));
  if (!docker.available) {
    out.appendChild(mk("div", "muted", docker.err || "Docker unavailable"));
  } else {
    const table = mk("table");
    const head = mk("tr");
    ["Container", "Image", "State", "CPU", "RAM", ""].forEach((h) => head.appendChild(mk("th", null, h)));
    table.appendChild(head);
    (docker.containers || []).forEach((c) => {
      const tr = mk("tr");
      tr.appendChild(mk("td", null, c.name));
      tr.appendChild(mk("td", "muted", c.image || ""));
      tr.appendChild(mk("td", null, c.state || ""));
      tr.appendChild(mk("td", null, c.cpu || ""));
      tr.appendChild(mk("td", null, c.mem || ""));
      const td = mk("td");
      const btn = mk("button", "small secondary", "logs");
      btn.addEventListener("click", () => showDockerLogs(hive, c.name));
      td.appendChild(btn);
      tr.appendChild(td);
      table.appendChild(tr);
    });
    out.appendChild(table);
  }

  const models = rep.models || {};
  out.appendChild(mk("h3", null, "Models & Ollama"));
  const lines = [];
  lines.push("Ollama available: " + (models.ollamaAvailable ? "yes" : "no"));
  if (models.configuredDefault) lines.push("Configured default: " + models.configuredDefault);
  if (models.mismatches && models.mismatches.length) lines.push("Mismatches: " + models.mismatches.join("; "));
  if (models.routeIssues && models.routeIssues.length) lines.push("Route issues: " + models.routeIssues.join("; "));
  if (models.ollamaModels && models.ollamaModels.length) lines.push("Ollama models: " + models.ollamaModels.map((m) => m.name).join(", "));
  out.appendChild(mk("pre", "output", lines.join("\n")));
}

async function showUnit(hive, unit) {
  try {
    const detail = await api(`/api/hive/unit?hive=${encodeURIComponent(hive)}&unit=${encodeURIComponent(unit)}`);
    $("#hive-raw").textContent = pretty(detail);
  } catch (e) {
    banner(e.message, "error");
  }
}

async function controlUnit(hive, unit, action) {
  const ok = window.confirm(`${action} ${unit} on ${hive}? This changes remote state.`);
  if (!ok) return;
  try {
    await api("/api/hive/unit/control", {
      method: "POST",
      body: { hive, unit, action, confirmed: true },
    });
    banner(`systemctl ${action} ${unit} completed.`, "ok");
    showServices();
  } catch (e) {
    banner(e.message, "error");
  }
}

async function showDockerLogs(hive, container) {
  try {
    const data = await api(`/api/hive/docker-logs?hive=${encodeURIComponent(hive)}&container=${encodeURIComponent(container)}&tail=200`);
    $("#hive-raw").textContent = data.logs || "(no logs)";
  } catch (e) {
    banner(e.message, "error");
  }
}

async function fetchLogs() {
  const hive = selectedHive("#log-hive");
  if (!hive) return;
  const source = $("#log-source").value;
  const target = $("#log-target").value;
  const q = new URLSearchParams({ hive, source, target, tail: "200" });
  banner("Fetching logs...");
  try {
    const res = await api(`/api/hive/logs?${q}`);
    renderLogs(res);
    banner("");
  } catch (e) {
    banner(e.message, "error");
  }
}

function renderLogs(res) {
  const out = $("#log-output");
  clear(out);
  const errorsOnly = $("#log-errors").checked;
  (res.entries || []).forEach((entry) => {
    if (errorsOnly && entry.level !== "error") return;
    const line = mk("div", "log-line " + (entry.level || "info"), entry.line);
    out.appendChild(line);
  });
  if (!out.firstChild) out.textContent = "(no log lines)";
}

async function loadJobs() {
  if (!state.meta || !state.meta.jobsPath) {
    $("#jobs-path").textContent = "Jobs disabled.";
    return;
  }
  state.jobs = (await api("/api/jobs")) || [];
  $("#jobs-path").textContent = state.meta.jobsPath;
  renderJobs();
}

function renderJobs() {
  const out = $("#jobs-list");
  clear(out);
  if (!state.jobs.length) {
    out.appendChild(mk("div", "panel muted", "No jobs yet. Add one below."));
    return;
  }
  const table = mk("table");
  const head = mk("tr");
  ["Job", "Kind", "Cron", "Next", "Last", "Status", ""].forEach((h) => head.appendChild(mk("th", null, h)));
  table.appendChild(head);
  state.jobs.forEach((st) => {
    const job = st.job;
    const tr = mk("tr");
    tr.appendChild(mk("td", null, job.name));
    tr.appendChild(mk("td", null, job.kind));
    tr.appendChild(mk("td", "muted", job.schedule));
    tr.appendChild(mk("td", "muted", fmtTime(st.nextRun)));
    const last = st.last || {};
    tr.appendChild(mk("td", last.err ? "bad" : "muted", last.err || last.summary || "never"));
    const status = mk("td");
    if (st.running) status.appendChild(mk("span", "badge info", "running"));
    else if (last.err) status.appendChild(mk("span", "badge fail", "failed"));
    else if (last.summary) status.appendChild(mk("span", "badge ok", "ok"));
    else status.appendChild(mk("span", "badge", "idle"));
    tr.appendChild(status);
    const actions = mk("td");
    if (!state.meta || !state.meta.readOnly) {
      const run = mk("button", "small primary", "Run");
      run.addEventListener("click", () => runJob(job.name));
      actions.appendChild(run);
      const edit = mk("button", "small secondary", "Edit");
      edit.addEventListener("click", () => editJobForm(job));
      actions.appendChild(edit);
      const del = mk("button", "small danger", "Delete");
      del.addEventListener("click", () => deleteJob(job.name));
      actions.appendChild(del);
    }
    tr.appendChild(actions);
    table.appendChild(tr);
  });
  out.appendChild(table);
}

function editJobForm(job) {
  state.editJob = job.name;
  $("#job-form-title").textContent = "Edit job: " + job.name;
  $("#job-name").value = job.name;
  $("#job-kind").value = job.kind;
  $("#job-schedule").value = job.schedule;
  $("#job-hive").value = job.hive || "";
  $("#job-output").value = job.output || "";
  $("#job-disabled").checked = !!job.disabled;
}

function resetJobForm() {
  state.editJob = null;
  $("#job-form-title").textContent = "Add job";
  $("#job-name").value = "";
  $("#job-kind").value = "scan";
  $("#job-schedule").value = "0 3 * * *";
  $("#job-hive").value = "";
  $("#job-output").value = "";
  $("#job-disabled").checked = false;
}

async function saveJob() {
  const job = {
    name: $("#job-name").value.trim(),
    kind: $("#job-kind").value,
    schedule: $("#job-schedule").value.trim(),
    hive: $("#job-hive").value.trim(),
    output: $("#job-output").value.trim(),
    disabled: $("#job-disabled").checked,
  };
  if (state.editJob !== job.name && state.jobs.some((st) => st.job.name === job.name)) {
    banner("A job with that name already exists.", "error");
    return;
  }
  try {
    state.jobs = await api("/api/jobs", { method: "POST", body: job });
    if (state.editJob && state.editJob !== job.name) {
      state.jobs = await api(`/api/jobs?name=${encodeURIComponent(state.editJob)}`, { method: "DELETE" });
    }
    renderJobs();
    resetJobForm();
    banner("Job saved.", "ok");
  } catch (e) {
    banner(e.message, "error");
  }
}

async function deleteJob(name) {
  if (!window.confirm(`Delete job ${name}?`)) return;
  try {
    state.jobs = await api(`/api/jobs?name=${encodeURIComponent(name)}`, { method: "DELETE" });
    renderJobs();
    banner("Job deleted.", "ok");
  } catch (e) {
    banner(e.message, "error");
  }
}

async function runJob(name) {
  try {
    await api(`/api/jobs/run?name=${encodeURIComponent(name)}`, { method: "POST" });
    banner(`Job ${name} started.`, "ok");
    pollJobs();
  } catch (e) {
    banner(e.message, "error");
  }
}

function pollJobs() {
  loadJobs().then(() => {
    if (state.jobs.some((st) => st.running)) setTimeout(pollJobs, 1500);
  }).catch(() => {});
}

async function loadConfig() {
  const data = await api("/api/config");
  state.config = data.config;
  $("#config-path").textContent = data.path || "";
  renderHives();
}

function hiveType(hive) {
  return hive.type || (hive.host ? "ssh" : "local");
}

function renderHives() {
  const out = $("#hives-list");
  clear(out);
  const hives = (state.config && state.config.hives) || [];
  if (!hives.length) {
    out.appendChild(mk("div", "panel muted", "No Hives configured."));
    return;
  }
  const table = mk("table");
  const head = mk("tr");
  ["Name", "Type", "Host", "User", "Port", "Label", ""].forEach((h) => head.appendChild(mk("th", null, h)));
  table.appendChild(head);
  hives.forEach((hive) => {
    const tr = mk("tr");
    tr.appendChild(mk("td", null, hive.name));
    tr.appendChild(mk("td", null, hiveType(hive)));
    tr.appendChild(mk("td", "muted", hive.host || ""));
    tr.appendChild(mk("td", "muted", hive.user || ""));
    tr.appendChild(mk("td", "muted", hive.port || ""));
    tr.appendChild(mk("td", "muted", hive.label || ""));
    const td = mk("td");
    if (!state.meta || !state.meta.readOnly) {
      const edit = mk("button", "small secondary", "Edit");
      edit.addEventListener("click", () => editHiveForm(hive));
      td.appendChild(edit);
      const del = mk("button", "small danger", "Delete");
      del.addEventListener("click", () => deleteHive(hive.name));
      td.appendChild(del);
    }
    tr.appendChild(td);
    table.appendChild(tr);
  });
  out.appendChild(table);
}

function readHiveForm() {
  const hive = {
    name: $("#hive-name").value.trim(),
    type: $("#hive-type").value,
    label: $("#hive-label").value.trim(),
  };
  if (hive.type === "ssh") {
    hive.host = $("#hive-host").value.trim();
    hive.user = $("#hive-user").value.trim();
    const port = Number($("#hive-port").value);
    if (port) hive.port = port;
  }
  return hive;
}

function editHiveForm(hive) {
  state.editHive = hive.name;
  $("#hive-form-title").textContent = "Edit Hive: " + hive.name;
  $("#hive-name").value = hive.name;
  $("#hive-type").value = hiveType(hive);
  $("#hive-host").value = hive.host || "";
  $("#hive-user").value = hive.user || "";
  $("#hive-port").value = hive.port || "";
  $("#hive-label").value = hive.label || "";
}

function resetHiveForm() {
  state.editHive = null;
  $("#hive-form-title").textContent = "Add Hive";
  $("#hive-name").value = "";
  $("#hive-type").value = "local";
  $("#hive-host").value = "";
  $("#hive-user").value = "";
  $("#hive-port").value = "";
  $("#hive-label").value = "";
  $("#hive-test-output").textContent = "";
}

async function saveHive() {
  if (!state.config) {
    banner("Hive settings are still loading.", "error");
    return;
  }
  const hive = readHiveForm();
  if (!hive.name) {
    banner("Hive name is required.", "error");
    return;
  }
  const hives = (state.config.hives || []).slice();
  const idx = hives.findIndex((h) => h.name === (state.editHive || hive.name));
  if (idx >= 0) {
    const previous = hives[idx];
    if (previous.controlMaster !== undefined) hive.controlMaster = previous.controlMaster;
    if (previous.controlPersist) hive.controlPersist = previous.controlPersist;
    hives[idx] = hive;
  } else {
    hives.push(hive);
  }
  try {
    const data = await api("/api/config", {
      method: "PUT",
      body: { version: state.config.version || 1, hives },
    });
    state.config = data.config;
    renderHives();
    resetHiveForm();
    banner("Hive configuration saved; rescan to refresh the fleet.", "ok");
  } catch (e) {
    banner(e.message, "error");
  }
}

async function deleteHive(name) {
  if (!state.config) {
    banner("Hive settings are still loading.", "error");
    return;
  }
  if (!window.confirm(`Delete Hive ${name}?`)) return;
  const hives = (state.config.hives || []).filter((h) => h.name !== name);
  try {
    const data = await api("/api/config", {
      method: "PUT",
      body: { version: state.config.version || 1, hives },
    });
    state.config = data.config;
    renderHives();
    banner("Hive removed; rescan to refresh the fleet.", "ok");
  } catch (e) {
    banner(e.message, "error");
  }
}

async function testHive() {
  const hive = readHiveForm();
  try {
    const res = await api("/api/hive/test", { method: "POST", body: hive });
    $("#hive-test-output").textContent = pretty(res);
  } catch (e) {
    banner(e.message, "error");
  }
}

async function startSwarm() {
  const mode = $("#swarm-mode").value;
  const hive = $("#swarm-hive").value || hiveNames()[0] || "";
  const body = { mode, hive };
  if (state.selected) body.bee = { hive: state.selected.hive, id: state.selected.id };
  if (mode === "selected_bee" && !state.selected) {
    banner("Select a Bee first from the Swarm tab.", "error");
    return;
  }
  try {
    const data = await api("/api/swarm", { method: "POST", body });
    state.swarm = { id: data.id };
    $("#swarm-cancel").classList.remove("hidden");
    banner(`Swarm Review started across ${data.bees} Bee(s).`, "ok");
    pollSwarm();
  } catch (e) {
    banner(e.message, "error");
  }
}

function pollSwarm() {
  if (!state.swarm) return;
  clearTimeout(state.swarmTimer);
  const tick = async () => {
    try {
      const data = await api(`/api/swarm?id=${encodeURIComponent(state.swarm.id)}`);
      renderSwarmProgress(data.snapshot);
      const done = data.snapshot.done;
      $("#swarm-cancel").classList.toggle("hidden", done);
      if (!done) state.swarmTimer = setTimeout(tick, 1000);
      else banner("Swarm Review finished.", "ok");
    } catch (e) {
      banner(e.message, "error");
    }
  };
  tick();
}

function renderSwarmProgress(snap) {
  const out = $("#swarm-progress");
  clear(out);
  if (!snap) return;
  const sum = snap.summary || {};
  out.appendChild(mk("div", "muted",
    `total=${sum.total ?? 0} running=${sum.running ?? 0} complete=${sum.completed ?? 0} failed=${sum.failed ?? 0} skipped=${sum.skipped ?? 0} proposals=${sum.totalProposals ?? 0}`));
  const table = mk("table");
  const head = mk("tr");
  ["Bee", "State", "Proposals", "Output"].forEach((h) => head.appendChild(mk("th", null, h)));
  table.appendChild(head);
  (snap.items || []).forEach((item) => {
    const tr = mk("tr");
    const bee = item.bee || {};
    tr.appendChild(mk("td", null, `${bee.hive || ""}/${bee.id || ""}`));
    const stateTd = mk("td");
    const kind = item.state === "complete" ? "ok" : item.state === "failed" ? "fail" : "info";
    stateTd.appendChild(mk("span", "badge " + kind, item.state || ""));
    tr.appendChild(stateTd);
    tr.appendChild(mk("td", null, item.proposalCount || 0));
    tr.appendChild(mk("td", "muted", item.err || item.output || ""));
    table.appendChild(tr);
  });
  out.appendChild(table);
}

async function cancelSwarm() {
  if (!state.swarm) return;
  try {
    await api(`/api/swarm/cancel?id=${encodeURIComponent(state.swarm.id)}`, { method: "POST" });
    banner("Swarm Review canceled; completed reviews are preserved.", "ok");
    pollSwarm();
  } catch (e) {
    banner(e.message, "error");
  }
}

async function exportReport() {
  try {
    const data = await api("/api/report", { method: "POST", body: {} });
    banner("Honey Report exported to " + data.path, "ok");
  } catch (e) {
    banner(e.message, "error");
  }
}

function applyMeta() {
  if (!state.meta) return;
  $("#version").textContent = state.meta.version || "";
  $("#readonly-pill").classList.toggle("hidden", !state.meta.readOnly);
  if (state.meta.readOnly) {
    ["#job-save", "#hive-save", "#swarm-start"].forEach((sel) => {
      const el = $(sel);
      if (el) el.disabled = true;
    });
  }
}

async function init() {
  try {
    state.meta = await api("/api/meta");
    applyMeta();
  } catch (e) {
    banner("Could not load BeeChief metadata: " + e.message, "error");
    return;
  }
  resetJobForm();
  resetHiveForm();
  setTab("swarm");
  await scan();
}

$("#tabs").addEventListener("click", (ev) => {
  const btn = ev.target.closest("button[data-tab]");
  if (btn) setTab(btn.dataset.tab);
});
$("#scan-btn").addEventListener("click", () => scan());
$("#report-btn").addEventListener("click", () => exportReport());
$("#health-btn").addEventListener("click", () => showHealth());
$("#services-btn").addEventListener("click", () => showServices());
$("#log-fetch").addEventListener("click", () => fetchLogs());
$("#log-errors").addEventListener("change", () => fetchLogs());
$("#job-save").addEventListener("click", () => saveJob());
$("#job-reset").addEventListener("click", () => resetJobForm());
$("#hive-save").addEventListener("click", () => saveHive());
$("#hive-test").addEventListener("click", () => testHive());
$("#hive-reset").addEventListener("click", () => resetHiveForm());
$("#swarm-start").addEventListener("click", () => startSwarm());
$("#swarm-cancel").addEventListener("click", () => cancelSwarm());
$("#swarm-mode").addEventListener("change", () => {
  $("#swarm-hive").disabled = $("#swarm-mode").value === "all_reachable";
});

init();
