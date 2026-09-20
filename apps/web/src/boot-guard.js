// Last resort, before the modules exist.
//
// If /src/app.js fails to load, or throws while it is evaluating, nothing in
// the console runs: no render, no error handler, no toast. The document stays
// exactly as index.html left it -- an empty #app on a dark background, with a
// tab title that says the page loaded. An operator reaching for the console
// mid-incident gets a black rectangle and no reason for it.
//
// A classic script, not a module and not inline: it has to run even when the
// module graph is broken, and the console is served under `script-src 'self'`,
// which refuses inline script.
//
// DOM calls rather than innerHTML. setHTML is the only path that puts HTML
// into this document, and it lives in a module that by definition has not
// loaded if this code is the one reporting.
(function () {
  var reported = false;

  function report(detail) {
    if (reported) return;
    var app = document.getElementById("app");
    // Something drew: either the console started, or it already failed once.
    if (!app || app.childElementCount) return;
    reported = true;

    var box = document.createElement("div");
    box.style.cssText =
      "max-width:640px;margin:16vh auto;padding:24px;border:1px solid #2a3442;" +
      "border-radius:10px;background:#111821;color:#cbd5e1;" +
      "font:14px/1.6 system-ui,sans-serif";

    var head = document.createElement("h1");
    head.textContent = "KloudView failed to start";
    head.style.cssText = "margin:0 0 8px;font-size:17px";

    var hint = document.createElement("p");
    hint.textContent =
      "The console could not load its code. Reload with a fresh copy (Ctrl+Shift+R). If it still fails, the server may be part-way through an update.";
    hint.style.margin = "0 0 12px";

    var why = document.createElement("pre");
    why.textContent = detail;
    why.style.cssText =
      "margin:0;white-space:pre-wrap;word-break:break-word;color:#8b98a8;" +
      "font:12px/1.5 ui-monospace,monospace";

    box.appendChild(head);
    box.appendChild(hint);
    box.appendChild(why);
    app.appendChild(box);
  }

  window.addEventListener("error", function (event) {
    report(
      String(
        (event.error && event.error.stack) || event.message || event.type,
      ),
    );
  });
  window.addEventListener("unhandledrejection", function (event) {
    report(String((event.reason && event.reason.stack) || event.reason));
  });
  // A module that fails to resolve raises nothing either handler can hear, and
  // neither does an exception inside boot's own error handling. Long enough
  // that a slow first load is not accused of failing.
  setTimeout(function () {
    report("The console loaded but drew nothing within 8 seconds.");
  }, 8000);
})();
