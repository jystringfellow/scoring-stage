"use strict";

const fs = require("fs");
const vm = require("vm");

const [pagePath, scenarioPath] = process.argv.slice(2);
const scenario = JSON.parse(fs.readFileSync(scenarioPath, "utf8"));

class FakeElement {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this.dataset = {};
    this.hidden = false;
    this.className = "";
    this.ownText = "";
  }

  set textContent(value) {
    this.children = [];
    this.ownText = String(value);
  }

  get textContent() {
    return this.ownText + this.children.map((child) => child.textContent).join("");
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  getAttribute(name) {
    return name in this.attributes ? this.attributes[name] : null;
  }

  appendChild(child) {
    this.children.push(child);
    return child;
  }

  replaceChildren(...children) {
    this.ownText = "";
    this.children = children;
  }

  addEventListener(type, listener) {
    (this.listeners[type] = this.listeners[type] || []).push(listener);
  }

  click() {
    for (const listener of this.listeners.click || []) {
      listener();
    }
  }

  all(tag) {
    const found = [];
    const walk = (node) => {
      for (const child of node.children) {
        if (child.tagName === tag) {
          found.push(child);
        }
        walk(child);
      }
    };
    walk(this);
    return found;
  }
}

const byID = {};
for (const id of ["status", "scoreboard", "graph", "stages", "rows"]) {
  byID[id] = new FakeElement(id === "rows" ? "tbody" : "section");
}
byID.graph.hidden = true;
byID.mode = new FakeElement("button");
const root = new FakeElement("html");
const stored = Object.assign({}, scenario.stored || {});
const storage = {
  getItem: (key) => (key in stored ? stored[key] : null),
  setItem: (key, value) => {
    stored[key] = String(value);
  },
};
const buttons = ["table", "graph"].map((view) => {
  const button = new FakeElement("button");
  button.dataset.view = view;
  return button;
});

function matches(query) {
  let all = true;
  for (const [, side, pixels] of query.matchAll(/(max|min)-width:\s*([0-9.]+)px/g)) {
    all = all && (side === "max" ? scenario.width <= Number(pixels) : scenario.width >= Number(pixels));
  }
  return all;
}

const context = vm.createContext({
  window: { matchMedia: (query) => ({ media: query, matches: matches(query), addEventListener() {} }) },
  document: {
    documentElement: root,
    createElement: (tag) => new FakeElement(tag),
    createElementNS: (namespace, tag) => new FakeElement(tag),
    getElementById: (id) => byID[id],
    querySelectorAll: (selector) => (selector === "nav button" ? buttons : []),
  },
  location: { hash: scenario.hash ? "#" + scenario.hash : "" },
  fetch: async () => ({ ok: true, json: async () => scenario.view }),
  setTimeout: () => {},
  URLSearchParams,
  localStorage: storage,
  getComputedStyle: (element) => ({
    getPropertyValue: (name) => (name === "--mode" ? element.getAttribute("data-theme") || scenario.default_mode || "dark" : ""),
  }),
});

vm.runInContext(fs.readFileSync(pagePath, "utf8"), context, { filename: pagePath });

setImmediate(() => {
  if (scenario.click === "mode") {
    byID.mode.click();
  } else if (scenario.click) {
    buttons.find((button) => button.dataset.view === scenario.click).click();
  }
  const graph = byID.graph;
  const pressed = buttons.find((button) => button.getAttribute("aria-pressed") === "true");
  process.stdout.write(JSON.stringify({
    status: byID.status.textContent,
    graph_hidden: graph.hidden,
    table_hidden: byID.stages.hidden,
    graph_text: graph.all("p").map((paragraph) => paragraph.textContent),
    drawn: graph.all("h2").map((heading) => heading.textContent),
    pictures: graph.all("svg").length,
    pressed: pressed ? pressed.dataset.view : "",
    code: byID.rows.all("code").map((code) => code.textContent),
    theme: root.getAttribute("data-theme") || "",
    stored: stored,
    mode_label: byID.mode.getAttribute("aria-label") || "",
  }));
});
