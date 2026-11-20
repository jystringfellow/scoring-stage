"use strict";

const pollEvery = 2000;
const columns = 8;
const expanded = new Set();
const openTails = new Set();
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
  cell(row, stage.summary.sessions, stateClass(stage.sessions.status));
  cell(row, stage.summary.activity);
  rows.appendChild(row);
  if (open) {
    addDetails(detailRow(rows), stage);
  }
}

function addDetails(parent, stage) {
  if (stage.read && stage.read.error) {
    parent.appendChild(el("p", "The last read failed: " + stage.read.error, "error"));
  } else if (stage.read) {
    parent.appendChild(el("p", "Read at " + stage.read.at + " in " + stage.read.took_ms + " ms", "muted"));
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

function addActivity(parent, stage, activity) {
  const heading = activity.provider + " · " + activity.title;
  parent.appendChild(el("h2", heading));
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

function showStatus(view) {
  const status = document.getElementById("status");
  status.textContent = "Read " + view.read_at + (view.error ? " · " + view.error : "");
  status.className = view.error ? "error" : "";
}

function render(view) {
  if (!view) {
    return;
  }
  latest = view;
  showStatus(view);
  const rows = document.getElementById("rows");
  rows.replaceChildren();
  const resting = [];
  for (const stage of view.stages) {
    if (stage.state !== "running" && stage.attention.length === 0) {
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
    const stages = JSON.stringify(view.stages);
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

poll();
