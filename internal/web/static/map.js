// Network map (/maps/{id}): switches and internet connections on a background
// image, links with their current rates, updated every few seconds. Admins
// move nodes, add and remove them, draw links by hand and hide automatic ones. Everything is
// drawn as SVG through the DOM (no inline styles; the page's CSP forbids them).
(function () {
  "use strict";
  var page = document.getElementById("map-page");
  if (!page) return;
  var select = document.getElementById("map-select");
  if (select) select.addEventListener("change", function () { window.location.href = select.value; });
  if (!page.dataset.state) return;

  var NS = "http://www.w3.org/2000/svg";
  var t = page.dataset;
  var admin = t.admin === "1";
  var csrf = document.body.dataset.csrf || "";
  var svg = document.getElementById("map-svg");
  var gBG = document.getElementById("map-bg"), gLinks = document.getElementById("map-links");
  var gLabels = document.getElementById("map-labels"), gNodes = document.getElementById("map-nodes");
  var updated = document.getElementById("map-updated");
  var editor = document.getElementById("map-editor");
  var state = null, editing = false, dragging = null, selected = null, timer = null;

  function el(name, attrs, parent) {
    var e = document.createElementNS(NS, name);
    Object.keys(attrs || {}).forEach(function (k) { e.setAttribute(k, attrs[k]); });
    if (parent) parent.appendChild(e);
    return e;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  function bps(v) {
    if (!(v > 0)) return "0";
    var u = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"], i = 0;
    while (v >= 1000 && i < u.length - 1) { v /= 1000; i++; }
    return (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + " " + u[i];
  }
  function speed(v) { return v ? bps(v).replace(".00 ", " ").replace("/s", "") : "?"; }
  function utilClass(u) {
    if (u < 1) return "util-0";
    if (u < 10) return "util-1";
    if (u < 25) return "util-2";
    if (u < 50) return "util-3";
    if (u < 75) return "util-4";
    return "util-5";
  }
  function nodeByKey(key) {
    for (var i = 0; i < state.nodes.length; i++) if (state.nodes[i].key === key) return state.nodes[i];
    return null;
  }
  function pos(n) { return { x: n.x * state.map.width, y: n.y * state.map.height }; }

  function post(path, body, json) {
    var headers = { "X-CSRF-Token": csrf };
    if (json) headers["Content-Type"] = "application/json";
    return fetch(t.base + path, { method: "POST", credentials: "same-origin", headers: headers,
      body: json ? JSON.stringify(body) : body });
  }

  function load() {
    if (dragging) return;
    fetch(t.state, { credentials: "same-origin", headers: { Accept: "application/json" } })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (s) {
        s.nodes = s.nodes || []; s.links = s.links || [];
        state = s; render();
      })
      .catch(function () { updated.textContent = t.tError; })
      .then(schedule, schedule);
  }
  function schedule() {
    if (timer) clearTimeout(timer);
    var secs = state && state.map.refresh ? state.map.refresh : 15;
    timer = setTimeout(load, secs * 1000);
  }

  function render() {
    var m = state.map;
    svg.setAttribute("viewBox", "0 0 " + m.width + " " + m.height);
    clear(gBG);
    if (m.bg) el("image", { href: m.bg, x: 0, y: 0, width: m.width, height: m.height }, gBG);
    else el("rect", { x: 0, y: 0, width: m.width, height: m.height, "class": "map-empty" }, gBG);
    var scale = Math.max(m.width, m.height) / 1600;
    renderLinks(scale);
    renderNodes(scale);
    updated.textContent = t.tUpdated + " " + new Date(state.time).toLocaleTimeString();
    if (admin) renderEditor();
  }

  function renderLinks(scale) {
    clear(gLinks);
    clear(gLabels);
    // Several links between the same two devices (a LAG-less double cable, a
    // manual link next to an automatic one) are drawn side by side.
    var pairs = {}, slot = [];
    (state.links || []).forEach(function (l, idx) {
      var k = l.a.node < l.b.node ? l.a.node + "-" + l.b.node : l.b.node + "-" + l.a.node;
      pairs[k] = pairs[k] || [];
      slot[idx] = { key: k, i: pairs[k].length };
      pairs[k].push(idx);
    });
    var gap = Math.max(10, 18 * scale);
    (state.links || []).forEach(function (l, idx) {
      var a = nodeByKey(l.a.node), b = nodeByKey(l.b.node);
      if (!a || !b) return;
      var pa = pos(a), pb = pos(b);
      var n = pairs[slot[idx].key].length;
      if (n > 1) {
        // Offset perpendicular to the line, measured in the direction from the
        // lower to the higher node key so that it does not depend on A/B.
        var lo = a.key < b.key ? pa : pb, hi = a.key < b.key ? pb : pa;
        var dx = hi.x - lo.x, dy = hi.y - lo.y, len = Math.sqrt(dx * dx + dy * dy) || 1;
        var off = (slot[idx].i - (n - 1) / 2) * gap;
        var ox = -dy / len * off, oy = dx / len * off;
        pa = { x: pa.x + ox, y: pa.y + oy };
        pb = { x: pb.x + ox, y: pb.y + oy };
      }
      var down = l.source === "wan" ? (l.b.ifindex && !l.b.up) :
        (l.a.ifindex && !l.a.up && l.rates) || a.status === "down" || b.status === "down";
      var cls = "map-link " + (down ? "down" : l.rates ? utilClass(l.util) : "unknown") + " src-" + l.source;
      if (selected && selected.kind === "link" && selected.idx === idx) cls += " selected";
      var line = el("line", { x1: pa.x, y1: pa.y, x2: pb.x, y2: pb.y, "class": cls,
        "stroke-width": Math.max(2, 5 * scale) }, gLinks);
      var title = el("title", {}, line);
      title.textContent = (l.source === "wan" ? a.name : a.name + " " + (l.a.port || t.tUnknownPort)) + " ⇄ " + b.name + " " + (l.b.port || t.tUnknownPort) +
        "\n" + (t["tSource" + l.source.charAt(0).toUpperCase() + l.source.slice(1)] || l.source) +
        " · " + speed(l.a.speed || l.b.speed) +
        (l.rates ? (l.source === "wan" ?
          "\n" + t.tWanDown + ": " + bps(l.ab_bps) + "\n" + t.tWanUp + ": " + bps(l.ba_bps) :
          "\n" + a.name + " → " + b.name + ": " + bps(l.ab_bps) + "\n" + b.name + " → " + a.name + ": " + bps(l.ba_bps)) : "");
      line.addEventListener("click", function () { if (editing) { selected = { kind: "link", idx: idx }; render(); } });
      if (l.rates) {
        label(pa, pb, 0.3, "▸ " + bps(l.ab_bps), scale);
        label(pa, pb, 0.7, "◂ " + bps(l.ba_bps), scale);
      }
    });
  }

  // label writes text along the line from pa to pb at fraction f; the arrow
  // ▸ points from A to B, ◂ from B to A (flipped when the text would be
  // upside down).
  function label(pa, pb, f, text, scale) {
    var x = pa.x + (pb.x - pa.x) * f, y = pa.y + (pb.y - pa.y) * f;
    var ang = Math.atan2(pb.y - pa.y, pb.x - pa.x) * 180 / Math.PI;
    if (ang > 90 || ang < -90) {
      ang += 180;
      text = text.replace("▸", "\u0000").replace("◂", "▸").replace("\u0000", "◂");
    }
    var g = el("g", { transform: "translate(" + x + " " + y + ") rotate(" + ang + ")" }, gLabels);
    var fs = Math.max(11, 15 * scale);
    var bg = el("rect", { "class": "map-label-bg", rx: 3 * scale }, g);
    var tx = el("text", { "class": "map-label", "text-anchor": "middle", y: -fs * 0.45, "font-size": fs }, g);
    tx.textContent = text;
    try {
      var bb = tx.getBBox();
      bg.setAttribute("x", bb.x - 3); bg.setAttribute("y", bb.y - 1);
      bg.setAttribute("width", bb.width + 6); bg.setAttribute("height", bb.height + 2);
    } catch (e) { /* not rendered yet */ }
  }

  function renderNodes(scale) {
    clear(gNodes);
    var w = 150 * scale, h = 40 * scale, fs = Math.max(12, 16 * scale);
    state.nodes.forEach(function (n) {
      var p = pos(n);
      var cls = "map-node kind-" + n.kind + " status-" + n.status + (n.href ? " linked" : "") +
        (selected && selected.kind === "node" && selected.key === n.key ? " selected" : "") + (editing ? " editing" : "");
      var g = el("g", { "class": cls, transform: "translate(" + p.x + " " + p.y + ")", tabindex: 0 }, gNodes);
      var shape = n.kind === "wan" ?
        el("ellipse", { cx: 0, cy: 0, rx: w / 2, ry: h * 0.7 }, g) :
        el("rect", { x: -w / 2, y: -h / 2, width: w, height: h, rx: 6 * scale }, g);
      var tx = el("text", { "text-anchor": "middle", "dominant-baseline": "central", "font-size": fs }, g);
      tx.textContent = n.name.length > 18 ? n.name.slice(0, 17) + "…" : n.name;
      var bw = Math.max(w, tx.getBBox().width + 16 * scale); // widen the shape to fit the name
      if (n.kind === "wan") { shape.setAttribute("rx", bw / 2); }
      else { shape.setAttribute("x", -bw / 2); shape.setAttribute("width", bw); }
      var title = el("title", {}, g);
      var hint = n.kind === "wan" ? t.tOpenWan : t.tOpen;
      title.textContent = n.name + (n.status === "archived" ? "\n" + t.tArchived : "") + (n.error ? "\n" + n.error : "") +
        (n.href && !editing ? "\n" + hint : "");
      g.addEventListener("pointerdown", function (e) { startDrag(e, n, g); });
      g.addEventListener("click", function () {
        if (editing) { selected = { kind: "node", key: n.key }; render(); return; }
        if (n.href) window.location.href = n.href;
      });
      g.addEventListener("keydown", function (e) {
        if (e.key === "Enter" && n.href && !editing) window.location.href = n.href;
      });
    });
  }

  // --- editing (admins) ---
  function svgPoint(e) {
    var pt = svg.createSVGPoint();
    pt.x = e.clientX; pt.y = e.clientY;
    return pt.matrixTransform(svg.getScreenCTM().inverse());
  }
  function startDrag(e, n, g) {
    if (!editing || e.button !== 0) return;
    e.preventDefault();
    var start = svgPoint(e), moved = false;
    dragging = { n: n, ox: n.x, oy: n.y };
    g.setPointerCapture(e.pointerId);
    function move(ev) {
      var p = svgPoint(ev);
      n.x = Math.min(1, Math.max(0, dragging.ox + (p.x - start.x) / state.map.width));
      n.y = Math.min(1, Math.max(0, dragging.oy + (p.y - start.y) / state.map.height));
      moved = true;
      var scale = Math.max(state.map.width, state.map.height) / 1600;
      g.setAttribute("transform", "translate(" + n.x * state.map.width + " " + n.y * state.map.height + ")");
      renderLinks(scale);
    }
    function up() {
      g.removeEventListener("pointermove", move);
      g.removeEventListener("pointerup", up);
      dragging = null;
      if (moved) saveNodes();
    }
    g.addEventListener("pointermove", move);
    g.addEventListener("pointerup", up);
  }
  function saveNodes(nodes) {
    var list = (nodes || state.nodes).map(function (n) { return { kind: n.kind, id: n.id, x: n.x, y: n.y }; });
    return post("/nodes", list, true).then(load);
  }

  function option(sel, value, text) {
    var o = document.createElement("option");
    o.value = value; o.textContent = text;
    sel.appendChild(o);
  }
  function renderEditor() {
    var add = document.getElementById("map-add-device");
    var keep = add.value;
    while (add.firstChild) add.removeChild(add.firstChild);
    (state.available || []).forEach(function (d) { option(add, d.kind + ":" + d.id, d.name); });
    add.value = keep;
    // manual link form: devices on the map and their ports
    var form = document.getElementById("map-link-form");
    ["a_switch", "b_switch"].forEach(function (name) {
      var sel = form.elements[name], prev = sel.value;
      while (sel.firstChild) sel.removeChild(sel.firstChild);
      option(sel, "", t.tPick);
      state.nodes.forEach(function (n) { if (n.kind === "switch") option(sel, n.id, n.name); });
      sel.value = prev;
      fillPorts(sel);
    });
    // selection
    var box = document.getElementById("map-selection");
    while (box.firstChild) box.removeChild(box.firstChild);
    if (selected && selected.kind === "node" && nodeByKey(selected.key)) {
      button(box, t.tRemove + ": " + nodeByKey(selected.key).name, "danger", function () {
        var key = selected.key;
        saveNodes(state.nodes.filter(function (n) { return n.key !== key; }));
        selected = null;
      });
    } else if (selected && selected.kind === "link" && state.links[selected.idx]) {
      var l = state.links[selected.idx];
      if (l.source === "manual") {
        button(box, t.tDeleteLink, "danger", function () { post("/links/" + l.id + "/delete", null).then(load); selected = null; });
      } else if (l.source !== "wan") {
        button(box, t.tHide, "", function () {
          var b = new URLSearchParams({ kind: "hidden", a_switch: l.a.device, a_if: l.a.ifindex, b_switch: l.b.device, b_if: l.b.ifindex });
          post("/links", b).then(load);
          selected = null;
        });
      }
    }
    var hidden = document.getElementById("map-hidden");
    while (hidden.firstChild) hidden.removeChild(hidden.firstChild);
    (state.hidden || []).forEach(function (h) {
      var li = document.createElement("li");
      var a = nodeByKey(h.a.node), b = nodeByKey(h.b.node);
      li.textContent = (a ? a.name : h.a.device) + " " + (h.a.port || "") + " – " + (b ? b.name : h.b.device) + " " + (h.b.port || "") + " ";
      button(li, t.tShow, "link", function () { post("/links/" + h.id + "/delete", null).then(load); });
      hidden.appendChild(li);
    });
  }
  function button(parent, text, cls, fn) {
    var b = document.createElement("button");
    b.type = "button"; b.textContent = text; if (cls) b.className = cls;
    b.addEventListener("click", fn);
    parent.appendChild(b);
  }
  function fillPorts(devSel) {
    var form = document.getElementById("map-link-form");
    var ports = form.elements[devSel.dataset.ports], prev = ports.value;
    while (ports.firstChild) ports.removeChild(ports.firstChild);
    option(ports, "0", t.tUnknownPort);
    var n = nodeByKey("s" + devSel.value);
    ((n && n.ports) || []).forEach(function (p) { option(ports, p.ifindex, p.name || String(p.ifindex)); });
    ports.value = prev || "0";
  }

  if (admin && editor) {
    var toggle = document.getElementById("map-edit-toggle");
    var setEditing = function (on) {
      editing = on;
      editor.hidden = !on;
      page.classList.toggle("map-editing", on);
      selected = null;
      if (state) render();
    };
    toggle.addEventListener("click", function () { setEditing(!editing); });
    if (/[?&]edit=1/.test(window.location.search)) setEditing(true);
    document.getElementById("map-add").addEventListener("click", function () {
      var v = document.getElementById("map-add-device").value.split(":");
      var id = Number(v[1]);
      if (!id || (v[0] !== "switch" && v[0] !== "wan")) return;
      var nodes = state.nodes.slice();
      nodes.push({ kind: v[0], id: id, x: 0.5, y: 0.5 });
      saveNodes(nodes);
    });
    var form = document.getElementById("map-link-form");
    ["a_switch", "b_switch"].forEach(function (name) {
      form.elements[name].addEventListener("change", function () { fillPorts(form.elements[name]); });
    });
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var f = form.elements;
      if (!f.a_switch.value || !f.b_switch.value || f.a_switch.value === f.b_switch.value) return;
      var b = new URLSearchParams({ kind: "manual", a_switch: f.a_switch.value, a_if: f.a_if.value,
        b_switch: f.b_switch.value, b_if: f.b_if.value });
      post("/links", b).then(load);
    });
  }
  load();
})();
