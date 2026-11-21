// Reads filled screens as a renderer does, for the tests of the screens module:
//
//   node driver.mjs <functions.js> <cases.jsonl>
//
// The first line of the cases is {"templates": {"<kind>/<screen>.<lang>": [parts…]}}. Each other
// line is a filled screen: {"t": "<kind>/<screen>.<lang>", "surface": "…", "data": {…}, "draw":
// true, "presses": [{"button": "…", "row": null, "set": {"/comment": "…"}, "ts": "…", "draw": true}]}.
//
// For each screen it prints one line. Drawing it (when `draw` is true) from `root` (a Show draws its child while `when`
// holds; a List a row for each item of its path, LineItems for each of its value, each read in its
// row; every property of each part drawn is resolved, its checks too), it says how many parts it
// drew, each value a part looks up in a table of the data (/people, /masters/…, /files) that the
// table does not have (`missing`), and each format of the basic catalog given null (`nulls`). For
// each press (on a copy of the data with `set` put in), the message the renderer sends: A2UI 1.0's
// {"version": "1.0", "action": {…}}, whether the button was drawn (when the press says `draw`), the
// messages of its checks that do not hold (`failed`: a renderer does not let it be pressed), or that
// it acts on the renderer alone (`local`). Anything that throws is said as `threw`.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [fnPath, casesPath] = process.argv.slice(2);
const { functions } = await import(pathToFileURL(fnPath).href);
const lines = readFileSync(casesPath, "utf8").split("\n").filter((l) => l !== "");
const parts = {};
for (const [name, list] of Object.entries(JSON.parse(lines[0]).templates)) {
  parts[name] = Object.fromEntries(list.map((p) => [p.id, p]));
}

const empty = (v) => v === null || v === undefined || v === "" || (Array.isArray(v) && v.length === 0);
const holds = (v) => v !== false && !empty(v);

function at(model, path, scope) {
  if (!path.startsWith("/")) {
    if (!scope) throw new Error(`the path ${path} is read outside a row`);
    return path === "" || path === "." ? scope.item : at(scope.item, "/" + path);
  }
  let x = model;
  for (const seg of path.split("/").slice(1)) {
    if (x === null || x === undefined) return undefined;
    x = x[seg.replace(/~1/g, "/").replace(/~0/g, "~")];
  }
  return x;
}

function put(model, path, v) {
  const segs = path.split("/").slice(1);
  let x = model;
  for (const seg of segs.slice(0, -1)) {
    if (x[seg] === null || typeof x[seg] !== "object") x[seg] = {};
    x = x[seg];
  }
  x[segs[segs.length - 1]] = v;
}

// resolver resolves values on a screen's data, as the renderer does, and keeps what it finds.
function resolver(model) {
  const missing = [];
  const nulls = [];
  const formatted = (name, id) => (args) => {
    if (args.value === null || args.value === undefined) nulls.push(`${id}: ${name}`);
    return `${name}(${JSON.stringify(args.value)})`;
  };
  const basic = (id) => ({
    required: ({ value }) => !empty(value),
    or: ({ values }) => values.some((v) => v === true),
    and: ({ values }) => values.every((v) => v === true),
    not: ({ value }) => value !== true,
    formatCurrency: formatted("formatCurrency", id),
    formatNumber: formatted("formatNumber", id),
    formatDate: formatted("formatDate", id),
    formatString: formatted("formatString", id),
    openUrl: () => null,
  });
  const value = (x, scope, id) => {
    if (x === null || typeof x !== "object") return x;
    if (Array.isArray(x)) return x.map((y) => value(y, scope, id));
    if (typeof x["@path"] === "string") return at(model, x["@path"], scope);
    if (typeof x["@call"] === "string") {
      if (x["@call"] === "@index") {
        if (!scope) throw new Error("@index is read outside a row");
        return scope.index;
      }
      const args = Object.fromEntries(Object.entries(x.args || {}).map(([k, v]) => [k, value(v, scope, id)]));
      const table = x.args && x.args.table;
      if (x["@call"] === "lookup" && table && typeof table["@path"] === "string") {
        for (const c of Array.isArray(args.value) ? args.value : [args.value]) {
          if (!empty(c) && !(args.table && Object.hasOwn(args.table, c))) missing.push([table["@path"], c, id]);
        }
      }
      const f = functions[x["@call"]] || basic(id)[x["@call"]];
      if (!f) throw new Error(`no function ${x["@call"]}`);
      return f(args);
    }
    return Object.fromEntries(Object.entries(x).map(([k, v]) => [k, value(v, scope, id)]));
  };
  return { value, missing, nulls };
}

// draw reads a screen from `root`: how many parts it drew, and each button's rows.
function draw(byId, model, r) {
  const buttons = {};
  let drawn = 0;
  const structure = new Set(["id", "component", "child", "children", "action"]);
  const walk = (id, scope) => {
    const c = byId[id];
    if (!c) throw new Error(`no part ${id}`);
    if (c.component === "Show") {
      if (holds(r.value(c.when, scope, id))) walk(c.child, scope);
      return;
    }
    drawn++;
    for (const [k, v] of Object.entries(c)) if (!structure.has(k)) r.value(v, scope, id);
    if (c.component === "Button") {
      (buttons[id] ||= []).push(scope);
      if (c.action && c.action.functionCall) r.value(c.action.functionCall, scope, id);
    }
    if (c.component === "LineItems") {
      const rows = r.value(c.value, scope, id);
      (Array.isArray(rows) ? rows : []).forEach((item, index) => walk(c.child, { item, index }));
      return;
    }
    if (c.children && !Array.isArray(c.children)) {
      const rows = at(model, c.children.path, scope);
      (Array.isArray(rows) ? rows : []).forEach((item, index) => walk(c.children.componentId, { item, index }));
      return;
    }
    if (typeof c.child === "string") walk(c.child, scope);
    if (Array.isArray(c.children)) c.children.forEach((x) => walk(x, scope));
  };
  walk("root", undefined);
  return { drawn, buttons };
}

function reaches(byId, from, id, seen = new Set()) {
  if (from === id) return true;
  if (seen.has(from)) return false;
  seen.add(from);
  const c = byId[from];
  if (!c) return false;
  const next = [];
  if (typeof c.child === "string") next.push(c.child);
  if (Array.isArray(c.children)) next.push(...c.children);
  else if (c.children) next.push(c.children.componentId);
  return next.some((x) => reaches(byId, x, id, seen));
}

function press(byId, surface, data, p) {
  const model = structuredClone(data);
  for (const [path, v] of Object.entries(p.set || {})) put(model, path, v);
  const c = byId[p.button];
  if (!c || c.component !== "Button") throw new Error(`no button ${p.button}`);
  if (!c.action.event) return { local: true };
  const row = p.row === undefined ? null : p.row;
  const r = resolver(model);
  let drawn = null;
  let scope;
  if (p.draw === true) {
    const visits = draw(byId, model, r).buttons[p.button] || [];
    const at = visits.findIndex((s) => (row === null ? s === undefined : s !== undefined && s.index === row));
    drawn = at >= 0;
    scope = drawn ? visits[at] : undefined;
  }
  if (scope === undefined && row !== null) {
    // not drawn there: the row of the list the button is in
    const list = Object.values(byId).find((x) => x.children && !Array.isArray(x.children) && reaches(byId, x.children.componentId, p.button));
    if (!list) throw new Error(`${p.button} is in no list`);
    const rows = at(model, list.children.path, undefined) || [];
    if (row >= rows.length) return { drawn: false, norow: true };
    scope = { item: rows[row], index: row };
  }
  // the checks that keep the button from being pressed, by their messages
  const failed = (c["@checks"] || []).filter((k) => r.value(k.condition, scope, p.button) !== true).map((k) => k.message);
  const context = r.value(c.action.event.context, scope, p.button);
  const message = {
    version: "1.0",
    action: { name: c.action.event.name, surfaceId: surface, sourceComponentId: p.button, timestamp: p.ts || "2026-11-16T00:00:00.000Z", context },
  };
  return { drawn, failed, message };
}

for (const line of lines.slice(1)) {
  const out = {};
  try {
    const sc = JSON.parse(line);
    const byId = parts[sc.t];
    if (!byId) throw new Error(`no template ${sc.t}`);
    if (sc.draw === true) {
      const r = resolver(sc.data);
      out.drawn = draw(byId, sc.data, r).drawn;
      out.missing = r.missing;
      out.nulls = r.nulls;
    }
    out.presses = (sc.presses || []).map((p) => {
      try {
        return press(byId, sc.surface, sc.data, p);
      } catch (e) {
        return { threw: String(e && e.stack ? e.stack : e) };
      }
    });
  } catch (e) {
    out.threw = String(e && e.stack ? e.stack : e);
  }
  console.log(JSON.stringify(out));
}
