"use strict";

const pollEvery = 2000;
const expanded = new Set();
const openTails = new Set();
const svgNamespace = "http://www.w3.org/2000/svg";
const graphColumn = 210;
const graphRow = 34;
const graphNode = 180;
const graphMinWidth = 360;
const tooNarrow = window.matchMedia("(max-width: " + (graphMinWidth - 1) + "px)");
const themeKey = "stage-hall-mode";
const guideKey = "stage-hall-guide";
const sessionOrder = ["working", "waiting", "idle", "exited", "unknown"];
let mode = "table";
let showResting = false;
let latest = null;
let drawnStages = "";

function el(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined && text !== null) {
    node.textContent = String(text);
  }
  if (className) {
    node.className = className;
  }
  return node;
}

function stateClass(state) {
  return "state-" + String(state || "unknown").replace(/[^a-z_]/g, "");
}

function words(state) {
  return String(state || "unknown").replace(/_/g, " ");
}

function stateText(state, raw) {
  return raw ? words(state) + " (" + raw + ")" : words(state);
}

function count(number, noun) {
  return number + " " + noun + (number === 1 ? "" : "s");
}

function remembered(key) {
  try {
    return localStorage.getItem(key);
  } catch (error) {
    return null;
  }
}

function remember(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch (error) {
    return;
  }
}

function addSection(parent, title) {
  parent.appendChild(el("h3", title));
  const list = el("ul");
  parent.appendChild(list);
  return list;
}

function copyButton(command) {
  const button = el("button", "Copy", "copy");
  button.type = "button";
  button.setAttribute("aria-label", "Copy " + command);
  button.addEventListener("click", () => {
    if (typeof navigator === "undefined" || !navigator.clipboard) {
      button.textContent = "Select it";
      return;
    }
    navigator.clipboard.writeText(command).then(
      () => {
        button.textContent = "Copied";
      },
      () => {
        button.textContent = "Select it";
      },
    );
  });
  return button;
}

function addCommand(parent, command, why) {
  const line = el("div", null, "command");
  line.appendChild(el("code", command));
  if (why) {
    line.appendChild(el("span", why, "why"));
  }
  line.appendChild(copyButton(command));
  parent.appendChild(line);
}

function addTile(parent, label, value, note, className) {
  const tile = el("div", null, className ? "tile " + className : "tile");
  tile.appendChild(el("span", label, "label"));
  tile.appendChild(el("span", value, "value"));
  tile.appendChild(el("span", note, "note"));
  parent.appendChild(tile);
}

function isLive(stage) {
  return stage.state === "running" && !stage.remembered;
}

function drawTiles(view) {
  const tiles = document.getElementById("tiles");
  tiles.replaceChildren();
  tiles.hidden = !view.stages.length;
  if (!view.stages.length) {
    return;
  }
  const live = view.stages.filter(isLive);
  const needing = view.stages.filter((stage) => stage.attention.length);
  const states = {};
  let sessions = 0;
  for (const stage of live) {
    for (const session of stage.sessions.items || []) {
      sessions += 1;
      states[session.state] = (states[session.state] || 0) + 1;
    }
  }
  const rank = (state) => (sessionOrder.includes(state) ? sessionOrder.indexOf(state) : sessionOrder.length);
  const breakdown = Object.keys(states).sort((a, b) => rank(a) - rank(b)).map((state) => states[state] + " " + words(state));
  const board = view.scoreboard || [];
  const total = (key) => board.reduce((sum, row) => sum + row[key], 0);
  addTile(tiles, "Running", live.length, "of " + count(view.stages.length, "stage"));
  addTile(tiles, "Needs you", needing.length, needing.slice(0, 2).map((stage) => stage.slug + ": " + stage.attention.join(", ")).join("; ") || "nothing waiting", needing.length ? "needs" : "");
  addTile(tiles, "Sessions", sessions, breakdown.join(" · ") || "none running");
  addTile(tiles, "Finished runs", total("runs"), total("succeeded") + " succeeded · " + total("failed") + " failed");
}

function addStage(list, stage) {
  const open = expanded.has(stage.slug);
  const article = el("article", null, stage.attention.length ? "stage attention" : "stage");
  const head = el("button", null, "stage-head");
  head.type = "button";
  head.setAttribute("aria-expanded", open ? "true" : "false");
  head.addEventListener("click", () => {
    if (expanded.has(stage.slug)) {
      expanded.delete(stage.slug);
    } else {
      expanded.add(stage.slug);
    }
    render(latest);
  });
  const arrow = el("span", open ? "▾" : "▸", "arrow");
  arrow.setAttribute("aria-hidden", "true");
  head.appendChild(arrow);
  head.appendChild(el("span", null, "dot " + stateClass(stage.state)));
  head.appendChild(el("span", stage.slug, "slug"));
  head.appendChild(el("span", words(stage.state), stateClass(stage.state)));
  for (const reason of stage.attention) {
    head.appendChild(el("span", reason, "badge"));
  }
  head.appendChild(el("span", stage.repository ? stage.repository + (stage.base_branch ? "@" + stage.base_branch : "") : "no task", "repo"));
  head.appendChild(el("span", null, "spacer"));
  head.appendChild(el("span", stage.remembered ? stage.summary.sessions + " (last read)" : stage.summary.sessions, "sessions"));
  const used = parseInt(stage.summary.disk, 10);
  if (!Number.isNaN(used)) {
    const disk = el("span", null, "disk");
    const meter = el("span", null, "meter");
    const fill = el("span");
    fill.style.width = Math.max(0, Math.min(100, used)) + "%";
    meter.appendChild(fill);
    disk.appendChild(meter);
    disk.appendChild(el("span", "disk " + stage.summary.disk));
    head.appendChild(disk);
  }
  head.appendChild(el("span", stage.summary.age, "age"));
  article.appendChild(head);

  const line = el("div", null, "activity-line");
  line.appendChild(el("span", stage.summary.activity, stage.remembered ? "activity muted" : "activity"));
  const stepped = (stage.activities || []).find((activity) => activity.steps && activity.steps.length);
  if (stepped) {
    for (const step of stepped.steps) {
      const mark = step.state === "done" ? "✓ " : step.state === "active" ? "● " : "○ ";
      line.appendChild(el("span", mark + step.name, "step step-" + String(step.state).replace(/[^a-z]/g, "")));
    }
  }
  article.appendChild(line);

  if (open) {
    const detail = el("div", null, "detail");
    addDetails(detail, stage);
    article.appendChild(detail);
  }
  list.appendChild(article);
}

function addDetails(parent, stage) {
  if (stage.remembered) {
    parent.appendChild(el("p", "Not running. This is what the hall last read, at " + stage.remembered + "; none of it is live.", "muted"));
  }
  if (stage.read && stage.read.error) {
    parent.appendChild(el("p", "The last read failed: " + stage.read.error, "error"));
  } else if (stage.read) {
    parent.appendChild(el("p", "Read at " + stage.read.at + " in " + stage.read.took_ms + " ms", "muted"));
  }
  if (stage.global_secrets && stage.global_secrets.length) {
    parent.appendChild(el("p", "sbx applies its global secrets to this stage, whatever its config says: " + stage.global_secrets.join(", ")));
  }
  if (stage.sessions.detail) {
    parent.appendChild(el("p", stage.sessions.detail, "muted"));
  }
  for (const failure of stage.provider_errors || []) {
    parent.appendChild(el("p", failure.provider + ": " + failure.error, "error"));
  }
  for (const activity of stage.activities || []) {
    addActivity(parent, stage, activity);
  }
  for (const earlier of stage.earlier_activities || []) {
    addActivity(parent, stage, earlier.activity, earlier.last_seen_at);
  }
  if (stage.unclaimed_sessions.length) {
    const list = addSection(parent, "Sessions");
    for (const session of stage.unclaimed_sessions) {
      addSession(list, stage, session);
    }
  }
  if (!stage.read && !stage.activities.length && !stage.unclaimed_sessions.length) {
    parent.appendChild(el("p", "Not read: stage reads only running stages it has a task for.", "muted"));
  }
  if (isLive(stage)) {
    parent.appendChild(el("h3", "In your terminal"));
    parent.appendChild(el("p", "The hall shows these commands and never runs them.", "muted"));
    addCommand(parent, "stage shell " + stage.slug, "a login shell in the repository");
    addCommand(parent, "stage ship " + stage.slug, "review its commit, then push it");
  }
}

function addTail(item, stage, session) {
  if (!session || !session.tail || !session.tail.length) {
    return;
  }
  const key = stage.slug + " " + session.source + " " + session.id;
  const tail = el("details", null, "tail");
  tail.open = openTails.has(key);
  tail.appendChild(el("summary", "terminal, last " + session.tail.length + (session.tail.length === 1 ? " line" : " lines")));
  const lines = el("pre", session.tail.join("\n"));
  tail.appendChild(lines);
  const showLatest = () => {
    lines.scrollTop = lines.scrollHeight;
  };
  tail.addEventListener("toggle", () => {
    if (tail.open === openTails.has(key)) {
      showLatest();
      return;
    }
    if (tail.open) {
      openTails.add(key);
      showLatest();
    } else {
      openTails.delete(key);
    }
  });
  item.appendChild(tail);
}

function sessionOf(stage, ref) {
  return (stage.sessions.items || []).find((session) => session.source === ref.source && session.id === ref.id);
}

function addAttach(item, stage, ref) {
  if (!ref || ref.source !== "cao" || !isLive(stage)) {
    return;
  }
  const line = el("div", null, "attach");
  addCommand(line, "stage attach " + stage.slug + " " + ref.id, "attach from a terminal; Ctrl-b d detaches, Ctrl-C goes to the agent");
  item.appendChild(line);
}

function addSession(list, stage, session) {
  const item = el("li");
  item.appendChild(el("span", session.name || session.id));
  item.appendChild(el("span", " · " + session.source + " " + session.id, "muted"));
  if (session.agent) {
    item.appendChild(el("span", " · " + session.agent, "muted"));
  }
  item.appendChild(el("span", " · "));
  item.appendChild(el("span", stateText(session.state, session.raw_state), stateClass(session.state)));
  addAttach(item, stage, session);
  addTail(item, stage, session);
  list.appendChild(item);
}

function addActivity(parent, stage, activity, lastSeen) {
  const heading = activity.provider + " · " + activity.title;
  parent.appendChild(el("h3", lastSeen ? "Earlier · " + heading : heading, "activity-title"));
  if (lastSeen) {
    parent.appendChild(el("p", "No longer reported; last seen at " + lastSeen + ". Its states are as they were then.", "muted"));
  }
  const facts = el("p");
  facts.appendChild(el("span", stateText(activity.status, activity.raw_status), stateClass(activity.status)));
  if (activity.round) {
    facts.appendChild(el("span", " · round " + activity.round.current + (activity.round.max ? "/" + activity.round.max : "")));
  }
  const tokens = activity.counters && activity.counters.tokens;
  if (tokens) {
    facts.appendChild(el("span", " · " + (tokens.at_least ? "≥" : "") + tokens.value + " tokens"));
  }
  if (activity.updated_at) {
    facts.appendChild(el("span", " · updated " + activity.updated_at, "muted"));
  }
  parent.appendChild(facts);
  if (activity.reason) {
    parent.appendChild(el("p", activity.reason));
  }
  for (const group of activity.groups || []) {
    const list = addSection(parent, group.name);
    for (const member of group.members) {
      const item = el("li");
      item.appendChild(el("span", null, "dot " + stateClass(member.state)));
      item.appendChild(el("span", " " + member.name));
      if (member.agent) {
        item.appendChild(el("span", " · " + member.agent, "muted"));
      }
      item.appendChild(el("span", " · "));
      item.appendChild(el("span", stateText(member.state, member.raw_state), stateClass(member.state)));
      if (member.session) {
        item.appendChild(el("span", " · " + member.session.source + " " + member.session.id, "muted"));
        addAttach(item, stage, member.session);
        addTail(item, stage, sessionOf(stage, member.session));
      }
      list.appendChild(item);
    }
  }
}

function svg(tag, attributes) {
  const node = document.createElementNS(svgNamespace, tag);
  for (const [name, value] of Object.entries(attributes || {})) {
    node.setAttribute(name, String(value));
  }
  return node;
}

function shorten(text, limit) {
  const characters = Array.from(String(text));
  return characters.length > limit ? characters.slice(0, limit - 1).join("") + "…" : characters.join("");
}

function drawGraph(section, stages) {
  section.replaceChildren();
  if (tooNarrow.matches) {
    section.appendChild(el("p", "The graph needs a window at least " + graphMinWidth + " px wide. Widen the window or zoom out; the table is below until then.", "error"));
    return;
  }
  let drawn = stages.filter((stage) => expanded.has(stage.slug));
  if (!drawn.length) {
    drawn = stages.filter((stage) => stage.running_jobs > 0);
    if (!drawn.length) {
      section.appendChild(el("p", "No stage is running a job. Expand a stage in the table to draw it here, or open the page with #view=graph&open=<stage>.", "muted"));
      return;
    }
    section.appendChild(el("p", "No stage is expanded, so this draws every stage with a running job. Expand stages in the table to choose others.", "muted"));
  }
  for (const stage of drawn) {
    const graph = stage.graph;
    section.appendChild(el("h2", stage.slug));
    const width = graph.columns * graphColumn;
    const height = Math.max(graph.rows, 1) * graphRow;
    const picture = svg("svg", { width: width, height: height, viewBox: "0 0 " + width + " " + height, role: "img" });
    const caption = svg("title");
    caption.textContent = "Stage " + stage.slug + ": activities, groups and members";
    picture.appendChild(caption);
    const where = new Map();
    for (const node of graph.nodes) {
      where.set(node.id, { x: node.column * graphColumn, y: node.row * graphRow + graphRow / 2 });
    }
    for (const edge of graph.edges) {
      const from = where.get(edge.from);
      const to = where.get(edge.to);
      if (!from || !to) {
        continue;
      }
      const bend = (to.x - from.x - graphNode) / 2;
      const path = "M" + (from.x + graphNode) + " " + from.y + " C" + (from.x + graphNode + bend) + " " + from.y + " " +
        (to.x - bend) + " " + to.y + " " + to.x + " " + to.y;
      picture.appendChild(svg("path", { d: path, class: "edge" }));
    }
    for (const node of graph.nodes) {
      const at = where.get(node.id);
      const group = svg("g", { class: "node node-" + node.kind });
      group.appendChild(svg("rect", { x: at.x, y: at.y - 12, width: graphNode, height: 24, rx: 5 }));
      let textX = at.x + 8;
      if (node.state) {
        group.appendChild(svg("circle", { cx: at.x + 12, cy: at.y, r: 5, class: "dot " + stateClass(node.state) }));
        textX = at.x + 22;
      }
      const label = svg("text", { x: textX, y: at.y + 4 });
      const room = node.state ? 24 : 26;
      const detail = node.detail ? shorten(node.detail, 12) : "";
      label.textContent = detail ? shorten(node.label, Math.max(room - detail.length - 3, 6)) + " · " + detail : shorten(node.label, room);
      group.appendChild(label);
      const title = svg("title");
      title.textContent = node.label + (node.detail ? " · " + node.detail : "") + (node.state ? " · " + words(node.state) : "") + (node.session ? " · " + node.session : "");
      group.appendChild(title);
      picture.appendChild(group);
    }
    section.appendChild(el("div", null, "graph-scroll")).appendChild(picture);
  }
}

function drawScoreboard(board) {
  const section = document.getElementById("scoreboard");
  section.replaceChildren();
  section.hidden = !board.length;
  if (!board.length) {
    return;
  }
  section.appendChild(el("h2", "Finished runs"));
  for (const row of board) {
    const run = el("div", null, "run");
    const head = el("div", null, "run-head");
    head.appendChild(el("code", row.repository));
    head.appendChild(el("span", (row.tokens_at_least ? "≥" : "") + Math.round(row.tokens).toLocaleString() + " tokens", "muted"));
    run.appendChild(head);
    const bar = el("div", null, "bar");
    for (const [part, value] of [["ok", row.succeeded], ["failed", row.failed]]) {
      const fill = el("span", null, part);
      fill.style.width = (row.runs ? (100 * value) / row.runs : 0) + "%";
      bar.appendChild(fill);
    }
    run.appendChild(bar);
    run.appendChild(el("span", count(row.runs, "run") + " · " + row.succeeded + " succeeded · " + row.failed + " failed · " + row.stopped + " stopped", "muted"));
    section.appendChild(run);
  }
}

function showStatus(view) {
  const status = document.getElementById("status");
  const problems = [view.error, view.global_secrets_error].filter(Boolean);
  status.textContent = ["Read " + view.read_at].concat(problems).join(" · ");
  status.className = problems.length ? "error" : "";
}

function addFirstRun(rows) {
  const first = el("section", null, "first-run");
  first.appendChild(el("h2", "No stages yet"));
  first.appendChild(el("p", "A stage is a sandbox where a coding agent works on one repository, with deny-all egress and its credentials held on the host. Start one from a terminal and it appears here by itself.", "muted"));
  const steps = el("ol");
  for (const [title, command] of [["Once per machine", "stage setup"], ["Start a stage", "stage up <repo> <slug>"], ["Run your agent or loop as a job", "stage exec --detach <slug> -- <command>"]]) {
    const step = el("li");
    step.appendChild(el("span", title));
    step.appendChild(el("code", command));
    steps.appendChild(step);
  }
  first.appendChild(steps);
  rows.appendChild(first);
}

function render(view) {
  if (!view) {
    return;
  }
  latest = view;
  showStatus(view);
  drawTiles(view);
  drawScoreboard(view.scoreboard || []);
  document.getElementById("where").textContent = "read-only · " + location.host + " · " + count(view.stages.length, "stage");
  for (const button of document.querySelectorAll("nav button")) {
    button.setAttribute("aria-pressed", button.dataset.view === mode ? "true" : "false");
  }
  document.getElementById("stages").hidden = mode === "graph" && !tooNarrow.matches;
  document.getElementById("graph").hidden = mode !== "graph";
  if (mode === "graph") {
    drawGraph(document.getElementById("graph"), view.stages);
  }
  const rows = document.getElementById("rows");
  rows.replaceChildren();
  if (!view.stages.length) {
    addFirstRun(rows);
    return;
  }
  const resting = [];
  for (const stage of view.stages) {
    if (stage.state !== "running" && stage.attention.length === 0 && !stage.remembered) {
      resting.push(stage);
    } else {
      addStage(rows, stage);
    }
  }
  if (resting.length) {
    const fold = el("div", null, "resting");
    fold.appendChild(el("span", resting.length + " not running: " + resting.map((stage) => stage.slug).join(", ")));
    const button = el("button", showResting ? "Hide" : "Show");
    button.type = "button";
    button.setAttribute("aria-expanded", showResting ? "true" : "false");
    button.addEventListener("click", () => {
      showResting = !showResting;
      render(latest);
    });
    fold.appendChild(button);
    rows.appendChild(fold);
    if (showResting) {
      for (const stage of resting) {
        addStage(rows, stage);
      }
    }
  }
}

async function poll() {
  try {
    const response = await fetch("api/hall", { cache: "no-store" });
    if (!response.ok) {
      throw new Error(response.status + " " + response.statusText);
    }
    const view = await response.json();
    const stages = JSON.stringify([view.stages, view.scoreboard]);
    if (stages === drawnStages) {
      latest = view;
      showStatus(view);
    } else {
      drawnStages = stages;
      render(view);
    }
  } catch (error) {
    const status = document.getElementById("status");
    status.textContent = "The hall stopped answering: " + error.message;
    status.className = "error";
  }
  setTimeout(poll, pollEvery);
}

function readStart() {
  const start = new URLSearchParams(location.hash.slice(1));
  if (start.get("view") === "graph") {
    mode = "graph";
  }
  for (const slug of (start.get("open") || "").split(",")) {
    if (slug) {
      expanded.add(slug);
    }
  }
}

function currentTheme() {
  return getComputedStyle(document.documentElement).getPropertyValue("--mode").trim() === "light" ? "light" : "dark";
}

function showThemeSwitch() {
  const next = currentTheme() === "dark" ? "light" : "dark";
  const button = document.getElementById("mode");
  button.textContent = next === "light" ? "Light" : "Dark";
  button.setAttribute("aria-label", "Switch to " + next + " mode");
}

function readStoredTheme() {
  const stored = remembered(themeKey);
  if (stored === "dark" || stored === "light") {
    document.documentElement.setAttribute("data-theme", stored);
  }
}

function showGuide(visible) {
  const toggle = document.getElementById("guide-toggle");
  document.getElementById("guide").hidden = !visible;
  toggle.textContent = visible ? "Hide guide" : "Guide";
  toggle.setAttribute("aria-expanded", visible ? "true" : "false");
}

document.getElementById("mode").addEventListener("click", () => {
  const next = currentTheme() === "dark" ? "light" : "dark";
  document.documentElement.setAttribute("data-theme", next);
  showThemeSwitch();
  remember(themeKey, next);
});

document.getElementById("guide-toggle").addEventListener("click", () => {
  const visible = document.getElementById("guide").hidden;
  showGuide(visible);
  remember(guideKey, visible ? "shown" : "hidden");
});

readStoredTheme();
showThemeSwitch();
showGuide(remembered(guideKey) !== "hidden");
readStart();
for (const button of document.querySelectorAll("nav button")) {
  button.addEventListener("click", () => {
    mode = button.dataset.view;
    render(latest);
  });
}
tooNarrow.addEventListener("change", () => render(latest));

poll();
