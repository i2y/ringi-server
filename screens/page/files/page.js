// The script of the plain HTML pages of ringi's screens. A page works without it: the server reads
// what is posted, checks it and shows the page again with what is wrong. With it, the page shows and
// hides the fields shown on a condition, marks the fields asked for on one, works out the computed
// fields, says what is wrong before anything is sent, and adds and removes rows without a round
// trip. It asks the functions ritsu writes for the kinds (functions.js, beside this file), so it
// checks what the A2UI screens check, the same way.
import { functions } from "./functions.js";

const MAX_EXACT = 9007199254740991n; // 2^53 - 1: the largest integer a browser holds exactly
const SENT_FOR = 3000; // how long a form sent waits for its answer before it may be sent again (ms)

// ── numbers and days, as the server reads them (number.go and read.go) ──

const WIDE = { "．": ".", "，": ",", "－": "-", "−": "-", "‐": "-", "＋": "+", "％": "%", "￥": "¥", "　": " " };

// Full-width digits and signs (what a Japanese input method types) written as ASCII.
export function halfWidth(s) {
  return String(s).replace(/[０-９．，－−‐＋％￥　]/g, (c) => {
    const code = c.charCodeAt(0);
    return code >= 0xff10 && code <= 0xff19 ? String.fromCharCode(code - 0xff10 + 48) : WIDE[c];
  });
}

const AMOUNT = /^([+-]?)([0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.([0-9]+))?$/;

// readNumber reads a number as a person types it, into the integer of steps of its unit
// (u: {num, den, code, yen}): {steps}, or {why} with "number", "step" or "range".
export function readNumber(typed, u) {
  let t = halfWidth(typed).trim();
  if (u.yen) {
    if (t.startsWith("¥")) t = t.slice(1).trim();
    if (t.endsWith("円")) t = t.slice(0, -1).trim();
  }
  if (u.code && t.endsWith(u.code)) t = t.slice(0, -u.code.length).trim();
  const m = AMOUNT.exec(t);
  if (!m) return { why: "number" };
  const frac = m[3] || "";
  let v = BigInt(m[2].replaceAll(",", "") + frac);
  if (m[1] === "-") v = -v;
  v *= BigInt(u.den);
  const div = 10n ** BigInt(frac.length) * BigInt(u.num);
  if (v % div !== 0n) return { why: "step" };
  const q = v / div;
  if (q > MAX_EXACT || q < -MAX_EXACT) return { why: "range" };
  return { steps: Number(q) };
}

// Commas between each three digits, in a number of five digits or more (2000, 20,000), as ritsu
// writes a number.
function group(digits) {
  return digits.length <= 4 ? digits : digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

// stepsText writes n steps as a person reads them: digits grouped by thousands, a point and the
// unit's decimals (u: {num, den, decimals}), without the unit.
export function stepsText(n, u, grouped = true) {
  let v = (BigInt(n) * BigInt(u.num) * 10n ** BigInt(u.decimals)) / BigInt(u.den);
  const neg = v < 0n;
  if (neg) v = -v;
  let digits = v.toString();
  if (digits.length <= u.decimals) digits = "0".repeat(u.decimals - digits.length + 1) + digits;
  let whole = digits.slice(0, digits.length - u.decimals);
  const frac = digits.slice(digits.length - u.decimals);
  if (grouped) whole = group(whole);
  const out = frac ? whole + "." + frac : whole;
  return neg ? "-" + out : out;
}

const SLASH_DAY = /^([0-9]{4})[/-]([0-9]{1,2})[/-]([0-9]{1,2})$/;

// readDate reads a day as a person types it: {day} written YYYY-MM-DD, or {why: "date"}.
export function readDate(typed) {
  let s = halfWidth(typed).trim();
  const m = SLASH_DAY.exec(s);
  if (m) s = `${m[1]}-${m[2].padStart(2, "0")}-${m[3].padStart(2, "0")}`;
  const d = /^([0-9]{4})-([0-9]{2})-([0-9]{2})$/.exec(s);
  if (!d) return { why: "date" };
  const [y, mo, day] = [Number(d[1]), Number(d[2]), Number(d[3])];
  const leap = (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0;
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (mo < 1 || mo > 12 || day < 1 || day > days[mo - 1]) return { why: "date" };
  return { day: s };
}

// ── white space and the words a part finds by ──

// The white space a comment and a part's words to find by are read without at their ends: Unicode's
// White_Space, the 25 code points the server reads as white space (Go's unicode.IsSpace, which the
// finds cut words at and the ticket machine reads a reason by) and ritsu's functions.js takes off a
// text and a comment (SPACE_ENDS). JavaScript's own trim takes U+FEFF off and leaves U+0085, so it is
// not used for them; a number and a day are read with it, as the server reads them (number.go,
// read.go) and functions.js reads a number.
const SPACE_ENDS = /^[\u0009-\u000D\u0020\u0085\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000]+|[\u0009-\u000D\u0020\u0085\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000]+$/g;

// trimSpace is a text without the white space at its ends (SPACE_ENDS).
export function trimSpace(s) {
  return String(s).replace(SPACE_ENDS, "");
}

// wordsFit reports whether the words a part finds by have at most longest characters, counted as the
// server counts them: in code points (a character past U+FFFF is one, not two of UTF-16).
export function wordsFit(words, longest) {
  let n = 0;
  for (const _ of words) if (++n > longest) return false;
  return true;
}

// ── a page ──

function unitOf(part) {
  const d = part.dataset;
  return { num: Number(d.num), den: Number(d.den), decimals: Number(d.decimals), code: d.code || "", yen: d.yen === "true" };
}

function call(name, args) {
  const f = functions[name];
  if (typeof f !== "function") return undefined;
  try {
    return f(args);
  } catch (e) {
    return undefined;
  }
}

// workedText is what the page shows for a computed field: the value its function in functions.js
// works out of the form, in its unit; "—" for one that is not a whole number of steps a browser
// holds exactly; undefined while the function gives nothing.
export function workedText(name, form, unit, format) {
  const v = call(name, { form });
  if (v === undefined) return undefined;
  return typeof v === "number" && Number.isSafeInteger(v) ? (format || "{}").replace("{}", stepsText(v, unit)) : "—";
}

// A part's input: the element its wrapper (field-<id>) names.
function inputOf(part) {
  return document.getElementById(part.id.slice("field-".length));
}

// What is wrong with what is typed in a part, before any check: "number", "step", "range", "date".
function readProblem(part) {
  const type = part.dataset.type;
  if (type !== "number" && type !== "date") return null;
  const el = inputOf(part);
  if (!el || el.value.trim() === "") return null;
  const r = type === "number" ? readNumber(el.value, unitOf(part)) : readDate(el.value);
  return r.why || null;
}

// A part's value, as the A2UI screens hold it: a number as its integer of steps, a choice as a list
// of one, rows as a list of rows. A value that cannot be read is kept as typed, for its checks.
function valueOf(part) {
  const type = part.dataset.type;
  if (type === "rows") return rowsOf(part).map((row) => valuesOf(row, true));
  const el = inputOf(part);
  if (!el) return null;
  switch (type) {
    case "number": {
      if (el.value.trim() === "") return null;
      const r = readNumber(el.value, unitOf(part));
      return r.why ? el.value : r.steps;
    }
    case "date": {
      if (el.value.trim() === "") return null;
      const r = readDate(el.value);
      return r.why ? el.value : r.day;
    }
    case "choice":
      return el.value === "" ? [] : [el.value];
    case "bool":
      return el.checked;
    case "attachment": {
      if (el.files && el.files.length > 0) return "file:" + el.files[0].name;
      const kept = part.querySelector('input[type="hidden"][name^="a."]');
      const removed = part.querySelector('input[type="checkbox"][name^="d."]');
      return kept && !(removed && removed.checked) ? kept.value : null;
    }
  }
  return el.value;
}

// The rows of a part of rows, in their order.
function rowsOf(rowsPart) {
  const list = rowsPart.querySelector(":scope > ol.row-list");
  return list ? [...list.children].map((li) => li.querySelector(":scope > fieldset.row")).filter(Boolean) : [];
}

// The values of the fields in an element: of a form, of an approver's edit, of a row.
function valuesOf(scope, inRow) {
  const out = {};
  for (const part of scope.querySelectorAll("[data-field]")) {
    const name = part.dataset.field;
    if (part.classList.contains("output")) continue;
    if (inRow) {
      if (part.closest("fieldset.row") !== scope) continue;
      out[name.slice(name.indexOf(".") + 1)] = valueOf(part);
    } else if (!name.includes(".")) {
      out[name] = valueOf(part);
    }
  }
  return out;
}

function setUp(form) {
  const kind = form.dataset.kind;
  const edit = form.querySelector("fieldset[data-edit]");
  let before = null;
  try {
    before = form.dataset.before ? JSON.parse(form.dataset.before) : null;
  } catch (e) {
    before = null;
  }
  const touched = new Set(); // the parts a person has changed
  let tried = null; // the scope of the last button pressed: its checks are said as they change
  // A form sent is not sent again until its answer comes, so that a button pressed twice makes one
  // request; a sending the person stops (Esc, the stop button) has no answer to come, and the form
  // may be sent again SENT_FOR after it, which the server answers as the first time when nothing
  // changed (a request's key is made from its screen and what it holds).
  let sent = false;
  let release = 0;
  form.classList.add("scripted");

  // The form the checks read: the form's values, or the ticket's form as the approver's edit leaves it.
  function checked() {
    if (!edit) return valuesOf(form, false);
    const e = valuesOf(edit, false);
    return call(kind + ".edited", { edit: e, before }) || Object.assign({}, before || {}, e);
  }

  function parts() {
    return [...form.querySelectorAll(".field")];
  }

  // Whether a part is checked for the button of a scope.
  function inScope(part, scope) {
    const id = part.id;
    const comment = id === "field-in-comment";
    switch (scope) {
      case "form":
        return comment || id === "field-in-requester" || (part.dataset.field !== undefined && !(edit && edit.contains(part)));
      case "edit":
        return comment || (edit !== null && edit.contains(part));
      case "approver":
        return comment || id === "field-in-approver";
      case "hand_over":
        return comment || id === "field-in-hand_over_of" || id === "field-in-hand_over_to";
    }
    return comment; // comment, given, reason
  }

  function rowOfCheck(li) {
    const row = li.closest("fieldset.row");
    if (!row) return undefined;
    const rows = rowsOf(row.closest("fieldset[data-rows]"));
    return rows.indexOf(row);
  }

  // Whether a check fails.
  function fails(li, part, values, scope) {
    const d = li.dataset;
    if (d.check) return call(kind + ".check", { form: values, code: d.check, row: rowOfCheck(li) }) === false;
    if (d.read) return readProblem(part) === d.read;
    const el = inputOf(part);
    const text = el ? el.value : "";
    switch (d.other) {
      case "fits":
        return call("commentFits", { text }) === false;
      case "given":
        return scope === "given" && call("commentGiven", { text }) === false;
      case "reason":
        // a button whose event the ticket's stage asks for a reason with
        return scope === "reason" && call("commentGiven", { text }) === false;
      case "required":
        return trimSpace(text) === "";
      case "may":
        return form.dataset.maySubmit === "false" && trimSpace(text) === "";
      case "refused":
      case "invalid":
        return !touched.has(part); // what the server said stands until the part changes
    }
    return false;
  }

  // The checks a part says now; one it cannot read says only that.
  function failing(part, values, scope) {
    if (part.hidden) return [];
    const lis = [...part.querySelectorAll(":scope > ul.checks > li")];
    const problem = readProblem(part);
    if (problem) {
      const read = lis.filter((li) => li.dataset.read === problem);
      return read.length > 0 ? read : lis.filter((li) => li.dataset.check && li.dataset.check.endsWith(".range"));
    }
    return lis.filter((li) => fails(li, part, values, scope));
  }

  function setShown(part, shown) {
    if (part.hidden === !shown) return;
    part.hidden = !shown;
    // a field hidden is not sent: the server reads it as hidden, and empties what it held
    for (const el of part.querySelectorAll("input, select, textarea, button")) el.disabled = !shown;
  }

  function update() {
    const values = checked();
    for (const part of form.querySelectorAll("[data-shown]")) {
      setShown(part, call(part.dataset.shown, { form: values }) !== false);
    }
    // a field asked for on a condition: its mark while the condition holds (not while it cannot be
    // worked out), as the server shows it; as the server showed it where functions.js has no function
    for (const part of form.querySelectorAll("[data-required-when]")) {
      const said = call(part.dataset.requiredWhen, { form: values });
      if (said === undefined) continue;
      const asked = said === true;
      const mark = part.querySelector(":scope > label > .required");
      if (mark) mark.hidden = !asked;
      const el = inputOf(part);
      if (el) {
        if (asked) el.setAttribute("aria-required", "true");
        else el.removeAttribute("aria-required");
      }
    }
    for (const part of form.querySelectorAll(".output[data-worked]")) {
      const out = inputOf(part);
      const text = workedText(part.dataset.worked, values, unitOf(part), part.dataset.format);
      if (out && text !== undefined) out.textContent = text;
    }
    for (const part of parts()) {
      const live = touched.has(part) || (tried !== null && inScope(part, tried)) || (part.classList.contains("output") && touched.size > 0);
      const says = live ? failing(part, values, tried) : null;
      for (const li of part.querySelectorAll(":scope > ul.checks > li")) {
        if (says !== null) li.hidden = !says.includes(li);
      }
      const el = inputOf(part);
      if (el && says !== null) {
        if (says.length > 0) el.setAttribute("aria-invalid", "true");
        else el.removeAttribute("aria-invalid");
      }
    }
  }

  function partOf(el) {
    return el.closest(".field");
  }

  function changed(e) {
    const part = partOf(e.target);
    if (part) {
      touched.add(part);
      // a row's field changes its rows' checks too
      const rows = part.closest("fieldset[data-rows]");
      if (rows && rows !== part) touched.add(rows);
    }
    update();
  }

  // A row's legend says its place among the rows (`Items, row 2`): the script writes the number
  // again, and keeps the rest, the rows' label marked with its language.
  function renumber(rowsPart) {
    rowsOf(rowsPart).forEach((row, i) => {
      const n = row.querySelector(":scope > legend > .row-n");
      if (n) n.textContent = String(i + 1);
    });
  }

  function addRow(rowsPart) {
    const list = rowsPart.querySelector(":scope > ol.row-list");
    const t = rowsPart.querySelector(":scope > template.row-template");
    if (!list || !t) return;
    let n = 0;
    for (const m of list.querySelectorAll(`input[type="hidden"][name="r.${rowsPart.dataset.rows}"]`)) {
      n = Math.max(n, Number(m.value) + 1);
    }
    const li = t.content.firstElementChild.cloneNode(true);
    for (const el of [li, ...li.querySelectorAll("*")]) {
      for (const a of [...el.attributes]) {
        if (a.value.includes("__n__")) el.setAttribute(a.name, a.value.replaceAll("__n__", String(n)));
      }
    }
    list.appendChild(li);
    renumber(rowsPart);
    touched.add(rowsPart);
    const first = li.querySelector("input:not([type=hidden]), select, textarea");
    if (first) first.focus();
  }

  function removeRow(li) {
    const rowsPart = li.closest("fieldset[data-rows]");
    li.remove();
    renumber(rowsPart);
    touched.add(rowsPart);
    const add = rowsPart.querySelector(":scope > button.add-row");
    if (add) add.focus();
  }

  // The words the page says when a button finds something wrong, at the head of the form.
  function summarize(found) {
    let box = form.querySelector(":scope > .summary.client");
    if (!box) {
      box = document.createElement("div");
      box.className = "summary client";
      box.setAttribute("role", "alert");
      box.tabIndex = -1;
      form.prepend(box);
    }
    box.replaceChildren();
    const ul = document.createElement("ul");
    for (const [part, li] of found) {
      const item = document.createElement("li");
      const a = document.createElement("a");
      const el = inputOf(part);
      a.href = "#" + (el ? el.id : part.id);
      // the check's words as the page has them: a name of another language keeps its mark (lang)
      a.append(...[...li.childNodes].map((n) => n.cloneNode(true)));
      item.append(a);
      ul.append(item);
    }
    box.append(ul);
    box.focus();
  }

  // A part that finds its value by a name (a picker with more values than a page lists): as a
  // person types, the values the words find are listed under it, as buttons that choose one; its
  // button Search finds at once. Without the script, the button posts the page, and the server
  // lists them.
  let findWords = {};
  try {
    findWords = form.dataset.findWords ? JSON.parse(form.dataset.findWords) : {};
  } catch (e) {
    findWords = {};
  }
  const finding = new Map(); // a part → its timer and the number of its last find

  // showFound says what the words typed found: one line, which a screen reader reads out when it
  // changes (the box's status, there since the page came), and the list to choose from after it.
  function showFound(part, typed, answer) {
    const box = part.querySelector(":scope > .found");
    const named = part.querySelector(":scope > .named");
    const find = part.querySelector("button.find");
    const status = box && box.querySelector(":scope > .status");
    if (!box || !named || !find || !status) return;
    for (const el of [...box.children]) if (el !== status) el.remove();
    const say = (...lines) => {
      // Japanese sentences follow one another with no space
      const sep = (part.closest("[lang]") || document.documentElement).lang === "ja" ? "" : " ";
      const text = lines.filter(Boolean).map((l) => l.replace("{}", typed)).join(sep);
      if (status.textContent !== text) status.textContent = text;
    };
    const found = answer && Array.isArray(answer.found) ? answer.found : [];
    if (found.length > 0 && found[0].value === typed) {
      // the value itself: its name
      named.textContent = nameOf(found[0]);
      named.hidden = !named.textContent;
      say();
      return;
    }
    named.textContent = "";
    named.hidden = true;
    if (typed === "") {
      say();
      return;
    }
    if (answer === LONG) {
      say(findWords.long);
      return;
    }
    if (!answer) {
      say(findWords.failed);
      return;
    }
    say(found.length > 0 ? findWords.found : findWords.none, answer.more ? findWords.more : "");
    if (found.length > 0) {
      const list = document.createElement("ul");
      const place = find.value.slice("find:".length);
      for (const o of found) {
        const b = document.createElement("button");
        b.type = "submit";
        b.name = "_do";
        b.value = "choose:" + place + ":" + o.value;
        b.className = "plain";
        b.dataset.value = o.value;
        b.dataset.name = nameOf(o);
        b.textContent = o.label;
        const li = document.createElement("li");
        li.append(b);
        list.append(li);
      }
      box.append(list);
    }
  }

  // The name said under a value found: none where the name is the value itself, which a find answers
  // as it is, with the value alone as its label.
  function nameOf(o) {
    return o.name && o.name !== o.value ? o.name : "";
  }

  // A find is asked with the words without the white space at their ends, as the server cuts
  // them (trimSpace), and not with more characters than the server takes (findWords.longest): such
  // words are said to be too long in the page's language (LONG), as a 400 of the find is.
  const LONG = {};
  const longest = Number(findWords.longest) || 200;

  async function find(part) {
    const el = inputOf(part);
    if (!el || !part.dataset.find) return;
    const state = finding.get(part) || { timer: 0, asked: 0 };
    finding.set(part, state);
    clearTimeout(state.timer);
    const asked = ++state.asked;
    const typed = trimSpace(el.value);
    let answer = { found: [], more: false };
    if (typed !== "" && !wordsFit(typed, longest)) {
      answer = LONG;
    } else if (typed !== "") {
      try {
        const res = await fetch(part.dataset.find + "&limit=20&q=" + encodeURIComponent(typed), {
          headers: { Accept: "application/json" },
          credentials: "same-origin",
        });
        answer = res.ok ? await res.json() : res.status === 400 ? LONG : null;
      } catch (e) {
        answer = null;
      }
    }
    if (state.asked !== asked) return; // a later find is on its way
    showFound(part, typed, answer);
  }

  function findSoon(part) {
    const state = finding.get(part) || { timer: 0, asked: 0 };
    finding.set(part, state);
    clearTimeout(state.timer);
    // the name said is of the value before: it goes until the find answers
    const named = part.querySelector(":scope > .named");
    if (named) {
      named.hidden = true;
      named.textContent = "";
    }
    state.timer = setTimeout(() => find(part), 250);
  }

  function choose(button) {
    const part = partOf(button);
    const el = part ? inputOf(part) : null;
    if (!el) return;
    const state = finding.get(part);
    if (state) {
      clearTimeout(state.timer);
      state.asked++;
    }
    el.value = button.dataset.value || "";
    const named = part.querySelector(":scope > .named");
    if (named) {
      named.textContent = button.dataset.name || "";
      named.hidden = !button.dataset.name;
    }
    const box = part.querySelector(":scope > .found");
    if (box) {
      for (const el of [...box.children]) {
        if (el.classList.contains("status")) el.textContent = "";
        else el.remove();
      }
    }
    touched.add(part);
    update();
    el.focus();
  }

  form.addEventListener("input", (e) => {
    const part = partOf(e.target);
    if (part && part.dataset.find && e.target === inputOf(part)) findSoon(part);
  });
  form.addEventListener("keydown", (e) => {
    // an Enter that ends an input method's text is not one to act on: composing, or (in browsers
    // that end the text first) 229
    if (e.key !== "Enter" || e.isComposing || e.keyCode === 229) return;
    const part = partOf(e.target);
    if (part && part.dataset.find && e.target === inputOf(part)) {
      e.preventDefault();
      find(part);
    }
  });

  form.addEventListener("input", changed);
  form.addEventListener("change", changed);
  form.addEventListener("submit", (e) => {
    const button = e.submitter;
    const value = button ? button.value : "";
    if (value === "stay") {
      e.preventDefault();
      return;
    }
    if (value.startsWith("find:") && button.closest("[data-find]")) {
      e.preventDefault();
      find(partOf(button));
      return;
    }
    if (value.startsWith("choose:")) {
      e.preventDefault();
      choose(button);
      return;
    }
    if (value.startsWith("row.add:")) {
      e.preventDefault();
      addRow(button.closest("fieldset[data-rows]"));
      update();
      return;
    }
    if (value.startsWith("row.remove:")) {
      e.preventDefault();
      removeRow(button.closest("li"));
      update();
      return;
    }
    if (sent) {
      e.preventDefault(); // sent already: its answer is on its way
      return;
    }
    tried = (button && button.dataset.scope) || "comment";
    update();
    const values = checked();
    const found = [];
    for (const part of parts()) {
      if (!inScope(part, tried)) continue;
      for (const li of failing(part, values, tried)) found.push([part, li]);
    }
    if (found.length > 0) {
      e.preventDefault();
      summarize(found);
      return;
    }
    sent = true;
    clearTimeout(release);
    release = setTimeout(() => {
      sent = false;
    }, SENT_FOR);
  });
  // a page the browser brings back (the back button, from its cache) is as it was before it was
  // sent: it may be sent again, which the server answers as the first time when nothing changed
  window.addEventListener("pageshow", (e) => {
    if (e.persisted) {
      sent = false;
      clearTimeout(release);
    }
  });
  // a page shown again goes to what it says first
  const summary = form.parentElement && form.parentElement.querySelector(":scope > .summary");
  if (summary) summary.focus();
  update();
}

if (typeof document !== "undefined") {
  for (const form of document.querySelectorAll("form[data-kind]")) setUp(form);
}
