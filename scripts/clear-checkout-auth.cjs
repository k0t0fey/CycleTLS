const assert = require("node:assert/strict");
const { spawnSync } = require("node:child_process");

function gitConfig(args, allowedStatuses) {
  const result = spawnSync("git", ["config", "--local", ...args], {
    encoding: "utf8",
  });
  if (result.error) throw result.error;
  assert(
    allowedStatuses.includes(result.status),
    `Local checkout credential cleanup failed with exit code ${result.status}`
  );
  return result.stdout.trim().split(/\r?\n/).filter(Boolean);
}

const origin = new URL(process.env.GITHUB_SERVER_URL || "https://github.com").origin;
const expectedKey = `http.${origin}/.extraheader`;
// Inspect key names only; credential values must never enter output.
const query = ["--name-only", "--get-regexp", "^http\\..*\\.extraheader$"];
const keys = gitConfig(query, [0, 1]);
assert(
  keys.every((key) => key === expectedKey),
  "Unexpected local HTTP credential configuration; refusing partial cleanup"
);
if (keys.length) gitConfig(["--unset-all", expectedKey], [0]);
assert.equal(gitConfig(query, [0, 1]).length, 0, "Checkout credentials remain");
console.log("PASS: no local HTTP checkout credentials remain before npm consumer");
