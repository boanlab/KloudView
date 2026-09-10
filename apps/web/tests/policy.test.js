import assert from "node:assert/strict";
import test from "node:test";

import {
  actionPermissions,
  hasPermission,
  permissionChecks,
} from "../src/policy.js";

test("action map covers destructive resource controls", () => {
  assert.deepEqual(actionPermissions["create-resource"], ["resources", "create"]);
  assert.deepEqual(actionPermissions["delete-resource"], ["resources", "delete"]);
  assert.deepEqual(actionPermissions["approve-terminal"], ["terminal", "approve"]);
});

test("explicit denial disables a permission", () => {
  assert.equal(hasPermission({ "resources:update": false }, "resources", "update"), false);
  assert.equal(hasPermission({ "resources:update": true }, "resources", "update"), true);
});

test("the fetched set covers permissions asked for outside the action map", () => {
  // can() is called with these directly; unfetched, they read as denied and the
  // control they gate disappears for everyone.
  const checks = permissionChecks();
  for (const key of ["relations:create", "relations:delete", "terminal:create", "terminal:approve-self"])
    assert.ok(checks.includes(key), `${key} is not fetched`);
  assert.equal(checks.length, new Set(checks).size, "checks contain duplicates");
});

test("an unknown permission is not a grant", () => {
  // The map is empty before the first sync, and stays empty if it fails.
  assert.equal(hasPermission({}, "resources", "delete"), false);
  assert.equal(hasPermission({ "alerts:read": true }, "resources", "delete"), false);
  assert.equal(hasPermission({ "resources:delete": undefined }, "resources", "delete"), false);
});
