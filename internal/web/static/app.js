// LANLeecher UI helpers. No inline scripts: the CSP only allows files from /static.
(function () {
  "use strict";

  // Confirm dialogs: <form data-confirm="..."> or <button data-confirm="...">.
  document.addEventListener("submit", function (e) {
    var form = e.target;
    var submitter = e.submitter;
    var msg = (submitter && submitter.dataset.confirm) || form.dataset.confirm;
    if (msg && !window.confirm(msg)) {
      e.preventDefault();
    }
  });

  // Auto-submit filter forms on change.
  document.addEventListener("change", function (e) {
    var form = e.target.closest && e.target.closest("form[data-autosubmit]");
    if (form) {
      form.requestSubmit();
    }
  });

  // "Select all" checkbox: <input type="checkbox" data-select-all="name">.
  document.addEventListener("change", function (e) {
    var name = e.target.dataset && e.target.dataset.selectAll;
    if (!name) return;
    var boxes = document.querySelectorAll('input[type="checkbox"][name="' + name + '"]');
    for (var i = 0; i < boxes.length; i++) boxes[i].checked = e.target.checked;
  });

  // Copy buttons: <button data-copy="element-id">.
  document.addEventListener("click", function (e) {
    var id = e.target.dataset && e.target.dataset.copy;
    if (!id) return;
    var el = document.getElementById(id);
    if (el && navigator.clipboard) {
      navigator.clipboard.writeText(el.textContent.trim()).then(function () {
        e.target.textContent = "✓";
      });
    }
  });

  // Show only the credential fields of the selected SNMP version.
  function syncVersion() {
    var sel = document.getElementById("snmp-version");
    if (!sel) return;
    var v3 = sel.value === "v3";
    document.querySelectorAll("fieldset.v3").forEach(function (f) { f.hidden = !v3; });
    document.querySelectorAll("fieldset.v2c").forEach(function (f) { f.hidden = v3; });
  }
  document.addEventListener("change", function (e) {
    if (e.target.id === "snmp-version") syncVersion();
  });
  document.addEventListener("DOMContentLoaded", syncVersion);

  // DHCP form: the v4/v6 selection only applies to the Control Agent.
  function syncKind() {
    var sel = document.getElementById("dhcp-kind");
    if (!sel) return;
    document.querySelectorAll("fieldset.agent-only").forEach(function (f) { f.hidden = sel.value !== "agent"; });
  }
  document.addEventListener("change", function (e) {
    if (e.target.id === "dhcp-kind") syncKind();
    // Checkboxes that need a confirmation when switched on.
    var msg = e.target.dataset && e.target.dataset.confirmCheck;
    if (msg && e.target.checked && !window.confirm(msg)) e.target.checked = false;
  });
  document.addEventListener("DOMContentLoaded", syncKind);
  // Chart tooltip: time and rate of the 5-minute bucket under the pointer.
  // lineChart (format.go) puts the values into data attributes of the SVG.
  var SVGNS = "http://www.w3.org/2000/svg";

  function formatBps(v) {
    var units = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"];
    var i = 0;
    while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
    return (i === 0 ? v.toFixed(0) : v.toFixed(1)) + " " + units[i];
  }

  function svgEl(name, cls) {
    var el = document.createElementNS(SVGNS, name);
    el.setAttribute("class", cls);
    return el;
  }

  function divEl(cls, text) {
    var el = document.createElement("div");
    el.className = cls;
    if (text !== undefined) el.textContent = text;
    return el;
  }

  function initChart(svg) {
    var wrap = svg.parentNode;
    var d = svg.dataset;
    var down = d.down.split(",").map(Number);
    var up = d.up.split(",").map(Number);
    var n = down.length;
    if (!n || n !== up.length) return;
    var start = Number(d.start), step = Number(d.step);
    var x0 = Number(d.x0), x1 = Number(d.x1), y0 = Number(d.y0), y1 = Number(d.y1), max = Number(d.max);
    var vb = svg.viewBox.baseVal;

    var legend = wrap.nextElementSibling;
    var lDown = (legend && legend.dataset.lDown) || "Download";
    var lUp = (legend && legend.dataset.lUp) || "Upload";
    var lang = document.documentElement.lang || undefined;
    var fmtDay = new Intl.DateTimeFormat(lang, { weekday: "short", day: "2-digit", month: "2-digit" });
    var fmtTime = new Intl.DateTimeFormat(lang, { hour: "2-digit", minute: "2-digit" });

    var marker = svgEl("g", "hover");
    var rule = svgEl("line", "hover-rule");
    rule.setAttribute("y1", y1);
    rule.setAttribute("y2", y0);
    var dotDown = svgEl("circle", "hover-dot-down");
    var dotUp = svgEl("circle", "hover-dot-up");
    dotDown.setAttribute("r", 4);
    dotUp.setAttribute("r", 4);
    marker.appendChild(rule);
    marker.appendChild(dotDown);
    marker.appendChild(dotUp);
    svg.appendChild(marker);

    var tip = divEl("chart-tip");
    var tTime = divEl("chart-tip-time");
    var tDown = divEl("chart-tip-row");
    var tUp = divEl("chart-tip-row");
    tip.appendChild(tTime);
    tip.appendChild(tDown);
    tip.appendChild(tUp);
    wrap.appendChild(tip);

    function row(el, cls, label, v) {
      el.textContent = "";
      var sw = document.createElement("span");
      sw.className = "swatch " + cls;
      el.appendChild(sw);
      el.appendChild(document.createTextNode(" " + label + " "));
      var b = document.createElement("b");
      b.textContent = formatBps(v);
      el.appendChild(b);
    }

    function y(v) { return y0 - (y0 - y1) * v / max; }

    function show(e) {
      var r = svg.getBoundingClientRect();
      if (!r.width) return;
      var vx = vb.x + (e.clientX - r.left) * vb.width / r.width;
      var i = n === 1 ? 0 : Math.round((vx - x0) / (x1 - x0) * (n - 1));
      i = Math.max(0, Math.min(n - 1, i));
      var px = n === 1 ? x0 : x0 + i * (x1 - x0) / (n - 1);
      rule.setAttribute("x1", px);
      rule.setAttribute("x2", px);
      dotDown.setAttribute("cx", px);
      dotDown.setAttribute("cy", y(down[i]));
      dotUp.setAttribute("cx", px);
      dotUp.setAttribute("cy", y(up[i]));
      marker.classList.add("on");

      var from = new Date((start + i * step) * 1000);
      var to = new Date((start + (i + 1) * step) * 1000);
      tTime.textContent = fmtDay.format(from) + " " + fmtTime.format(from) + "–" + fmtTime.format(to);
      row(tDown, "swatch-down", lDown, down[i]);
      row(tUp, "swatch-up", lUp, up[i]);

      // Position next to the marker, flipped to the left near the right edge.
      tip.classList.add("on");
      var left = (px - vb.x) * r.width / vb.width;
      var top = (Math.min(y(down[i]), y(up[i])) - vb.y) * r.height / vb.height;
      var tw = tip.offsetWidth, th = tip.offsetHeight;
      var tl = left + 12;
      if (tl + tw > r.width) tl = left - 12 - tw;
      tip.style.left = Math.max(0, tl) + "px";
      tip.style.top = Math.max(0, Math.min(r.height - th, top - th / 2)) + "px";
    }

    function hide() {
      marker.classList.remove("on");
      tip.classList.remove("on");
    }

    svg.addEventListener("pointermove", show);
    svg.addEventListener("pointerdown", show);
    svg.addEventListener("pointerleave", hide);

    if (d.detail) initDetail(svg, n, start, step, x0, x1, y0, y1, vb);
  }

  // Chart detail: a click into the chart selects its 5-minute bucket, a drag
  // selects a range. The server renders who caused the traffic in that range
  // (/partials/traffic-window) into the element named by data-detail-target.
  // The selection survives the periodic refresh of the dashboard.
  var selections = {};

  function initDetail(svg, n, start, step, x0, x1, y0, y1, vb) {
    var d = svg.dataset;
    var band = svgEl("rect", "selection");
    band.setAttribute("y", y1);
    band.setAttribute("height", Math.max(0, y0 - y1));
    svg.insertBefore(band, svg.firstChild);
    svg.classList.add("clickable");
    var half = n > 1 ? (x1 - x0) / (n - 1) / 2 : 6;

    function px(i) { return n === 1 ? x0 : x0 + i * (x1 - x0) / (n - 1); }

    function index(e) {
      var r = svg.getBoundingClientRect();
      var vx = vb.x + (e.clientX - r.left) * vb.width / r.width;
      var i = n === 1 ? 0 : Math.round((vx - x0) / (x1 - x0) * (n - 1));
      return Math.max(0, Math.min(n - 1, i));
    }

    function draw(lo, hi) {
      var a = Math.max(vb.x, px(lo) - half), b = Math.min(vb.x + vb.width, px(hi) + half);
      band.setAttribute("x", a);
      band.setAttribute("width", Math.max(1, b - a));
      band.classList.add("on");
    }

    var anchor = null;
    svg.addEventListener("pointerdown", function (e) {
      if (e.button !== undefined && e.button !== 0) return;
      anchor = index(e);
      // Keep receiving moves while the pointer drags outside of the chart.
      if (svg.setPointerCapture) svg.setPointerCapture(e.pointerId);
      draw(anchor, anchor);
    });
    svg.addEventListener("pointermove", function (e) {
      if (anchor === null) return;
      var i = index(e);
      draw(Math.min(anchor, i), Math.max(anchor, i));
    });
    svg.addEventListener("pointercancel", function () { anchor = null; restore(); });
    svg.addEventListener("pointerup", function (e) {
      if (anchor === null) return;
      var i = index(e);
      var lo = Math.min(anchor, i), hi = Math.max(anchor, i);
      anchor = null;
      var sel = { from: start + lo * step, to: start + (hi + 1) * step };
      selections[d.detail] = sel;
      draw(lo, hi);
      load(d.detail, d.detailTarget, sel);
    });

    // Redraw a kept selection after the chart was re-rendered.
    function restore() {
      var sel = selections[d.detail];
      if (!sel) { band.classList.remove("on"); return; }
      var lo = Math.round((sel.from - start) / step), hi = Math.round((sel.to - start) / step) - 1;
      if (hi < 0 || lo > n - 1) { band.classList.remove("on"); return; }
      draw(Math.max(0, lo), Math.min(n - 1, hi));
    }
    restore();
  }

  function load(url, targetID, sel) {
    var target = document.getElementById(targetID);
    if (!target || !window.htmx) return;
    window.htmx.ajax("GET", url + "&from=" + sel.from + "&to=" + sel.to, { target: target, swap: "innerHTML" });
  }

  document.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest("[data-detail-close]");
    if (!btn) return;
    var box = btn.closest("[id]");
    if (!box) return;
    box.textContent = "";
    var charts = document.querySelectorAll('svg.chart[data-detail-target="' + box.id + '"]');
    for (var i = 0; i < charts.length; i++) {
      delete selections[charts[i].dataset.detail];
      var band = charts[i].querySelector("rect.selection");
      if (band) band.classList.remove("on");
    }
  });

  function initCharts(root) {
    var charts = (root || document).querySelectorAll("svg.chart[data-down]:not([data-tip])");
    for (var i = 0; i < charts.length; i++) {
      charts[i].setAttribute("data-tip", "");
      initChart(charts[i]);
    }
  }
  document.addEventListener("DOMContentLoaded", function () { initCharts(); });
  // Charts inside content that htmx swaps in.
  document.addEventListener("htmx:afterSettle", function (e) { initCharts(e.target); });
})();
