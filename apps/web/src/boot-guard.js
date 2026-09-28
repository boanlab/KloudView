// Last resort, before the modules exist: if app.js fails to load or throws
// while evaluating, nothing else in the console runs and the document stays an
// empty #app.
//
// A classic script, not a module and not inline — it has to run with the module
// graph broken, under `script-src 'self'`. DOM calls rather than innerHTML,
// because setHTML lives in a module that has not loaded if this is reporting.
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
