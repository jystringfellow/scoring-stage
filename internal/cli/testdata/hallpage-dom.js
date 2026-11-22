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
    this.style = {};
    this.hidden = false;
    this.open = false;
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

  fire(type) {
    for (const listener of this.listeners[type] || []) {
      listener();
    }
  }

  click() {
    this.fire("click");
  }

  showModal() {
    this.open = true;
  }

  close() {
    if (this.open) {
      this.open = false;
      this.fire("close");
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
for (const id of ["status", "tiles", "where", "rows", "scoreboard", "guide", "graph-title", "graph-facts", "graph-body", "graph-link"]) {
  byID[id] = new FakeElement("section");
}
for (const id of ["guide-toggle", "mode", "graph-close"]) {
  byID[id] = new FakeElement("button");
}
byID["graph-dialog"] = new FakeElement("dialog");
const root = new FakeElement("html");
const copied = [];
const stored = Object.assign({}, scenario.stored || {});
const storage = {
  getItem: (key) => (key in stored ? stored[key] : null),
  setItem: (key, value) => {
    stored[key] = String(value);
  },
};
const location = { hash: scenario.hash ? "#" + scenario.hash : "", host: "127.0.0.1:7420", origin: "http://127.0.0.1:7420", pathname: "/token/", search: "" };

const context = vm.createContext({
  window: { matchMedia: () => ({ matches: false, addEventListener() {} }) },
  document: {
    documentElement: root,
    createElement: (tag) => new FakeElement(tag),
    getElementById: (id) => byID[id],
  },
  location: location,
  history: {
    replaceState: (state, title, url) => {
      const at = String(url).indexOf("#");
      location.hash = at < 0 ? "" : String(url).slice(at);
    },
  },
  navigator: {
    clipboard: {
      writeText: async (text) => {
        copied.push(String(text));
      },
    },
  },
  fetch: async () => ({ ok: true, json: async () => scenario.view }),
  setTimeout: () => {},
  URLSearchParams,
  localStorage: storage,
  getComputedStyle: (element) => ({
    getPropertyValue: (name) => (name === "--mode" ? element.getAttribute("data-theme") || scenario.default_mode || "dark" : ""),
  }),
});

vm.runInContext(fs.readFileSync(pagePath, "utf8"), context, { filename: pagePath });

function act(action) {
  const [verb, ...rest] = action.split(" ");
  const argument = rest.join(" ");
  if (verb === "mode") {
    byID.mode.click();
  } else if (verb === "guide") {
    byID["guide-toggle"].click();
  } else if (verb === "close") {
    byID["graph-close"].click();
  } else if (verb === "escape") {
    byID["graph-dialog"].close();
  } else if (verb === "copy") {
    const lines = byID.rows.all("div").concat(byID["graph-body"].all("div"));
    const line = lines.find((part) => part.className === "command" && part.all("code").some((code) => code.textContent === argument));
    line.all("button")[0].click();
  } else if (verb === "graph") {
    byID.rows.all("button").find((button) => button.className === "open-graph" && button.dataset.slug === argument).click();
  } else if (verb === "agent") {
    byID["graph-body"].all("button").find((button) => button.dataset.label === argument).click();
  } else {
    throw new Error("unknown action " + action);
  }
}

setImmediate(() => {
  for (const action of scenario.clicks || []) {
    act(action);
  }
  const body = byID["graph-body"];
  const picked = body.all("button").find((button) => button.getAttribute("aria-pressed") === "true");
  process.stdout.write(JSON.stringify({
    status: byID.status.textContent,
    code: byID.rows.all("code").map((code) => code.textContent),
    theme: root.getAttribute("data-theme") || "",
    stored: stored,
    mode_label: byID.mode.getAttribute("aria-label") || "",
    tiles: byID.tiles.children.map((tile) => tile.children.map((part) => part.textContent)),
    rows_text: byID.rows.textContent,
    open_details: byID.rows.all("div").filter((part) => part.className === "detail").length,
    copied: copied,
    guide_hidden: byID.guide.hidden,
    guide_label: byID["guide-toggle"].textContent,
    where: byID.where.textContent,
    hash: location.hash,
    dialog: {
      open: byID["graph-dialog"].open,
      title: byID["graph-title"].textContent,
      facts: byID["graph-facts"].textContent,
      labels: body.all("span").filter((span) => span.className === "label").map((span) => span.textContent),
      picked: picked ? picked.dataset.label : "",
      code: body.all("code").map((code) => code.textContent),
      tail: body.all("pre").map((pre) => pre.textContent).join("\n"),
      text: body.textContent,
      link: byID["graph-link"].textContent,
    },
  }));
});
