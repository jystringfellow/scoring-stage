"use strict";

const pollEvery = 2000;
const columns = 8;
const expanded = new Set();
const openTails = new Set();
const svgNamespace = "http://www.w3.org/2000/svg";
const graphColumn = 210;
const graphRow = 34;
const graphNode = 180;
const graphMinWidth = 360;
const tooNarrow = window.matchMedia("(max-width: " + (graphMinWidth - 1) + "px)");
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

function cell(row, text, className) {
  const td = el("td", text, className);
  row.appendChild(td);
  return td;
}

function detailRow(rows) {
  const row = el("tr", null, "detail");
  const td = cell(row, null);
  td.colSpan = columns;
  rows.appendChild(row);
  return td;
}

function addSection(parent, title) {
  parent.appendChild(el("h2", title));
  const list = el("ul");
  parent.appendChild(list);
  return list;
}

function stateText(state, raw) {
  return raw ? words(state) + " (" + raw + ")" : words(state);
}

function addStage(rows, stage) {
  const open = expanded.has(stage.slug);
  const row = el("tr", null, stage.attention.length ? "stage attention" : "stage");
  const toggle = el("button", open ? "▾" : "▸");
  toggle.setAttribute("aria-expanded", open ? "true" : "false");
  toggle.setAttribute("aria-label", "Details for " + stage.slug);
  toggle.addEventListener("click", () => {
    if (expanded.has(stage.slug)) {
      expanded.delete(stage.slug);
    } else {
      expanded.add(stage.slug);
    }
    render(latest);
  });
  cell(row, null).appendChild(toggle);
  const slug = cell(row, stage.slug);
  for (const reason of stage.attention) {
    slug.appendChild(el("span", reason, "badge"));
  }
  cell(row, words(stage.state), stateClass(stage.state));
  const repository = stage.repository ? stage.repository + (stage.base_branch ? "@" + stage.base_branch : "") : "-";
  cell(row, repository);
  cell(row, stage.summary.age);
  cell(row, stage.summary.disk);
  cell(row, stage.remembered ? stage.summary.sessions + " (last read)" : stage.summary.sessions, stage.remembered ? "muted" : stateClass(stage.sessions.status));
  cell(row, stage.summary.activity);
  rows.appendChild(row);
  if (open) {
    addDetails(detailRow(rows), stage);
  }
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

function addSession(list, stage, session) {
  const item = el("li");
  item.appendChild(el("span", session.name || session.id));
  item.appendChild(el("span", " · " + session.source + " " + session.id, "muted"));
  if (session.agent) {
    item.appendChild(el("span", " · " + session.agent, "muted"));
  }
  item.appendChild(el("span", " · "));
  item.appendChild(el("span", stateText(session.state, session.raw_state), stateClass(session.state)));
  addTail(item, stage, session);
  list.appendChild(item);
}

function addActivity(parent, stage, activity, lastSeen) {
  const heading = activity.provider + " · " + activity.title;
  parent.appendChild(el("h2", lastSeen ? "Earlier · " + heading : heading));
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
  if (activity.steps && activity.steps.length) {
    const steps = el("p");
    activity.steps.forEach((step, index) => {
      if (index > 0) {
        steps.appendChild(el("span", " → ", "muted"));
      }
      steps.appendChild(el("span", step.name, stateClass(step.state === "active" ? "running" : step.state)));
    });
    parent.appendChild(steps);
  }
  for (const group of activity.groups || []) {
    const list = addSection(parent, group.name);
    for (const member of group.members) {
      const item = el("li");
      item.appendChild(el("span", member.name));
      if (member.agent) {
        item.appendChild(el("span", " · " + member.agent, "muted"));
      }
      item.appendChild(el("span", " · "));
      item.appendChild(el("span", stateText(member.state, member.raw_state), stateClass(member.state)));
      if (member.session) {
        item.appendChild(el("span", " · " + member.session.source + " " + member.session.id, "muted"));
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
  const table = el("table", null, "scoreboard");
  const head = el("tr");
  for (const title of ["Repository", "Runs", "Succeeded", "Failed", "Stopped", "Tokens"]) {
    head.appendChild(el("th", title));
  }
  table.appendChild(el("thead")).appendChild(head);
  const body = el("tbody");
  for (const row of board) {
    const line = el("tr");
    line.appendChild(el("td", row.repository));
    line.appendChild(el("td", row.runs));
    line.appendChild(el("td", row.succeeded, row.succeeded ? "state-succeeded" : ""));
    line.appendChild(el("td", row.failed, row.failed ? "state-failed" : ""));
    line.appendChild(el("td", row.stopped));
    line.appendChild(el("td", (row.tokens_at_least ? "≥" : "") + Math.round(row.tokens).toLocaleString()));
    body.appendChild(line);
  }
  table.appendChild(body);
  section.appendChild(table);
}

function showStatus(view) {
  const status = document.getElementById("status");
  const problems = [view.error, view.global_secrets_error].filter(Boolean);
  status.textContent = ["Read " + view.read_at].concat(problems).join(" · ");
  status.className = problems.length ? "error" : "";
}

function render(view) {
  if (!view) {
    return;
  }
  latest = view;
  showStatus(view);
  drawScoreboard(view.scoreboard || []);
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
  const resting = [];
  for (const stage of view.stages) {
    if (stage.state !== "running" && stage.attention.length === 0 && !stage.remembered) {
      resting.push(stage);
    } else {
      addStage(rows, stage);
    }
  }
  if (resting.length) {
    const td = detailRow(rows);
    const label = (showResting ? "Hide " : "Show ") + resting.length + " not running: " + resting.map((stage) => stage.slug).join(", ");
    const button = el("button", label);
    button.setAttribute("aria-expanded", showResting ? "true" : "false");
    button.addEventListener("click", () => {
      showResting = !showResting;
      render(latest);
    });
    td.appendChild(button);
    if (showResting) {
      for (const stage of resting) {
        addStage(rows, stage);
      }
    }
  }
  if (!view.stages.length) {
    detailRow(rows).appendChild(el("p", "No stages.", "muted"));
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

readStart();
for (const button of document.querySelectorAll("nav button")) {
  button.addEventListener("click", () => {
    mode = button.dataset.view;
    render(latest);
  });
}
tooNarrow.addEventListener("change", () => render(latest));

poll();
