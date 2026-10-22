// LANLeecher OBS overlay. Polls GET /api/v1/board every 10 seconds.
// (EventSource cannot send an Authorization header, so polling is used here;
// the beamer page /board itself uses the SSE stream.)
(function () {
  "use strict";
  var p = new URLSearchParams(location.hash.slice(1));
  var base = (p.get("url") || "").replace(/\/$/, "");
  var token = p.get("token") || "";
  var limit = p.get("limit") || "10";
  var rows = parseInt(p.get("rows") || "5", 10);
  var show = p.get("show") || "both";
  var status = document.getElementById("status");

  if (!base || !token) {
    status.textContent = "Set #url=…&token=… in the browser source URL";
    return;
  }
  if (show === "down") document.getElementById("up").classList.add("hidden");
  if (show === "up") document.getElementById("down").classList.add("hidden");

  function bytes(v) {
    var u = ["B", "kB", "MB", "GB", "TB"], i = 0;
    while (v >= 1000 && i < u.length - 1) { v /= 1000; i++; }
    return v.toFixed(v >= 100 || i === 0 ? 0 : 1) + " " + u[i];
  }

  function fill(id, entries, field) {
    var ol = document.querySelector("#" + id + " ol");
    ol.textContent = "";
    entries.slice(0, rows).forEach(function (e) {
      var li = document.createElement("li");
      var name = document.createElement("span");
      name.textContent = e.rank + ". " + e.name; // names come from the network: textContent only
      var value = document.createElement("span");
      value.className = "value";
      value.textContent = bytes(e[field]);
      li.append(name, value);
      ol.appendChild(li);
    });
  }

  function update() {
    fetch(base + "/api/v1/board?limit=" + encodeURIComponent(limit), {
      headers: { Authorization: "Bearer " + token },
    })
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.json();
      })
      .then(function (b) {
        fill("down", b.top_download, "bytes_down");
        fill("up", b.top_upload, "bytes_up");
        status.textContent = b.event ? b.event.name : "no event running";
      })
      .catch(function (err) { status.textContent = "LANLeecher: " + err.message; });
  }

  update();
  setInterval(update, 10000);
})();
