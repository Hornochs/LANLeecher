// Beamer view: renders exactly what GET /api/v1/board returns, live via SSE.
(function () {
  "use strict";
  var body = document.body;
  var L = body.dataset;
  var params = new URLSearchParams({ limit: L.limit || "10" });
  if (L.event) params.set("event", L.event);

  function fmtBytes(v) {
    var units = ["B", "kB", "MB", "GB", "TB", "PB"];
    var i = 0;
    while (Math.abs(v) >= 1000 && i < units.length - 1) { v /= 1000; i++; }
    return (i === 0 ? v.toFixed(0) : v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + " " + units[i];
  }
  function fmtBps(v) {
    var units = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"];
    var i = 0;
    while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
    return v.toFixed(i === 0 ? 0 : 1) + " " + units[i];
  }
  function text(id, value) { document.getElementById(id).textContent = value; }

  function renderList(id, entries, field) {
    var ol = document.getElementById(id);
    ol.textContent = "";
    entries.forEach(function (e) {
      var li = document.createElement("li");
      var rank = document.createElement("span");
      rank.className = "rank";
      rank.textContent = e.rank + ".";
      var move = document.createElement("span");
      if (e.rank_change > 0) { move.className = "move-up"; move.textContent = "▲"; }
      else if (e.rank_change < 0) { move.className = "move-down"; move.textContent = "▼"; }
      var name = document.createElement("span");
      name.className = "name";
      name.textContent = e.name; // textContent: names come from the network
      var value = document.createElement("span");
      value.className = "value " + (field === "bytes_down" ? "down" : "up");
      value.textContent = fmtBytes(e[field]);
      li.append(rank, move, name, value);
      ol.appendChild(li);
    });
  }

  function render(b) {
    text("board-title-down", L.lDown);
    text("board-title-up", L.lUp);
    text("board-clients-label", L.lClients);
    if (!b.event) {
      text("board-event", L.lNone);
      ["board-down", "board-up", "board-clients", "board-down-bps", "board-up-bps"].forEach(function (id) { text(id, "–"); });
      renderList("board-list-down", [], "bytes_down");
      renderList("board-list-up", [], "bytes_up");
      return;
    }
    text("board-event", b.event.name);
    text("board-down", fmtBytes(b.totals.bytes_down));
    text("board-up", fmtBytes(b.totals.bytes_up));
    text("board-clients", String(b.totals.clients));
    text("board-down-bps", fmtBps(b.throughput.down_bps));
    text("board-up-bps", fmtBps(b.throughput.up_bps));
    renderList("board-list-down", b.top_download, "bytes_down");
    renderList("board-list-up", b.top_upload, "bytes_up");
    text("board-status", new Date(b.generated_at).toLocaleTimeString());
  }

  function poll() {
    fetch("/api/v1/board?" + params, { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(render)
      .catch(function () { text("board-status", L.lOffline); });
  }

  if (window.EventSource) {
    var es = new EventSource("/api/v1/board/stream?" + params);
    es.addEventListener("board", function (ev) { render(JSON.parse(ev.data)); });
    es.onerror = function () { text("board-status", L.lOffline); };
  } else {
    poll();
    setInterval(poll, 15000);
  }
})();
