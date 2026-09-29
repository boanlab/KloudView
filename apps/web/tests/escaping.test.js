import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../src/app.js", import.meta.url), "utf8");

// Text a person typed reaches the page through a template literal, so the
// escape is the only thing between a name and the markup it can become. The
// sanitiser in setHTML drops handlers and script tags, which stops the name
// executing but not the elements it introduces.
const TYPED_FIELDS =
  "(name|hostname|username|displayName|title|description|reason|summary|label)";

test("text a person typed is escaped where it is interpolated", () => {
  const pattern = new RegExp(
    "\\$\\{((?:[A-Za-z_$][\\w$]*)\\??\\." + TYPED_FIELDS + ")\\}",
    "g",
  );
  const unescaped = [];
  source.split("\n").forEach((line, index) => {
    for (const match of line.matchAll(pattern)) {
      const root = match[1].split(".")[0];
      if (["state", "location", "window", "document", "console"].includes(root)) continue;
      unescaped.push(`line ${index + 1}: \${${match[1]}}`);
    }
  });
  assert.deepEqual(unescaped, [], "interpolate these through escapeHTML");
});

test("free text inside an attribute is escaped, or it closes the attribute", () => {
  const pattern = /(?:value|title|placeholder|alt|aria-label)="\$\{([^{}]+)\}"/g;
  // What a person typed, not what the code wrote. A label the source picks
  // from a fixed pair is not free text.
  const freeText = /\b(query|name|hostname|username|displayName|description|reason|summary)\b/i;
  const unescaped = [];
  source.split("\n").forEach((line, index) => {
    for (const match of line.matchAll(pattern)) {
      if (match[1].includes("escapeHTML")) continue;
      if (!freeText.test(match[1])) continue;
      unescaped.push(`line ${index + 1}: ${match[0].slice(0, 60)}`);
    }
  });
  assert.deepEqual(unescaped, [], "interpolate these through escapeHTML");
});
