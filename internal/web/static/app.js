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
})();
