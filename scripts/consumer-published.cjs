const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const childProcess = require("node:child_process");
const { createRequire } = require("node:module");
const smoke = require("./smoke-local.cjs");
const metadata = require("../package.json");

const helpers = [
  "index",
  "index.exe",
  "index-arm64",
  "index-arm",
  "index-mac",
  "index-mac-arm64",
  "index-freebsd",
];

function installPublicPackage(directory) {
  const alias = `npm:${metadata.name}@${metadata.version}`;
  fs.writeFileSync(
    path.join(directory, "package.json"),
    JSON.stringify({
      name: "cycletls-public-consumer",
      version: "1.0.0",
      private: true,
      dependencies: { cycletls: alias },
    }) + "\n"
  );
  const userConfig = path.join(directory, ".npmrc");
  const globalConfig = path.join(directory, "global.npmrc");
  fs.writeFileSync(userConfig, "registry=https://registry.npmjs.org/\n");
  fs.writeFileSync(globalConfig, "");
  // Use public registry configuration only. Never inspect or print token values.
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([name]) =>
        !/^npm_config_/i.test(name) &&
        !/^(NODE_AUTH_TOKEN|NPM_TOKEN|NPM_AUTH_TOKEN|CYCLETLS)$/i.test(name)
    )
  );
  Object.assign(env, {
    NPM_CONFIG_USERCONFIG: userConfig,
    NPM_CONFIG_GLOBALCONFIG: globalConfig,
    NPM_CONFIG_REGISTRY: "https://registry.npmjs.org/",
    NPM_CONFIG_CACHE: path.join(directory, "npm-cache"),
  });
  for (const args of [
    ["install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"],
    ["ci", "--no-audit", "--no-fund"],
    ["audit", "signatures"],
  ]) {
    const result = childProcess.spawnSync(process.platform === "win32" ? "npm.cmd" : "npm", args, {
      cwd: directory,
      env,
      shell: process.platform === "win32",
      stdio: "inherit",
      timeout: 180000,
    });
    if (result.error) throw result.error;
    assert.equal(result.status, 0, `npm ${args.join(" ")} failed`);
  }
}

function verifyPackage(directory) {
  const consumerRequire = createRequire(path.join(directory, "package.json"));
  const installedRoot = path.join(directory, "node_modules", "cycletls");
  const installedManifest = consumerRequire.resolve("cycletls/package.json");
  assert.equal(
    fs.realpathSync(path.dirname(installedManifest)),
    fs.realpathSync(installedRoot),
    "Reject an ancestor node_modules fallback"
  );
  const installed = consumerRequire("cycletls/package.json");
  assert.equal(installed.name, metadata.name);
  assert.equal(installed.version, metadata.version);
  const lock = JSON.parse(fs.readFileSync(path.join(directory, "package-lock.json"), "utf8"));
  const locked = lock.packages["node_modules/cycletls"];
  assert.equal(locked.name, metadata.name);
  assert.equal(locked.version, metadata.version);
  assert(locked.integrity?.startsWith("sha512-"), "Missing package integrity");
  const checksums = new Map();
  for (const line of fs
    .readFileSync(path.join(installedRoot, "dist/checksums.txt"), "utf8")
    .trim()
    .split(/\r?\n/)) {
    const entry = /^([0-9a-f]{64})\s+([a-z0-9.-]+)$/.exec(line);
    assert(entry, "Invalid checksums.txt line");
    assert(!checksums.has(entry[2]), "Duplicate checksum");
    checksums.set(entry[2], entry[1]);
  }
  assert.deepEqual([...checksums.keys()].sort(), [...helpers].sort());
  for (const filename of helpers) {
    const binary = path.join(installedRoot, "dist", filename);
    const actual = crypto.createHash("sha256").update(fs.readFileSync(binary)).digest("hex");
    assert.equal(actual, checksums.get(filename), `SHA256 mismatch: ${filename}`);
    if (process.platform !== "win32" && filename !== "index.exe") {
      assert(fs.statSync(binary).mode & 0o111, `Executable bit missing: ${filename}`);
    }
  }
  for (const filename of ["LICENSE", "FORK.md", "dist/index.d.ts"]) {
    assert(fs.existsSync(path.join(installedRoot, filename)), `Missing ${filename}`);
  }
  return {
    init: consumerRequire("cycletls"),
    executable: path.join(
      installedRoot,
      "dist",
      process.platform === "win32" ? "index.exe" : "index"
    ),
  };
}

async function main() {
  assert(["win32", "linux"].includes(process.platform));
  assert.equal(process.arch, "x64");
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "cycletls-consumer-"));
  installPublicPackage(directory);
  const { init, executable } = verifyPackage(directory);
  const originalSpawn = childProcess.spawn;
  let launches = 0;
  childProcess.spawn = function observedSpawn(command, ...args) {
    if (path.resolve(String(command)) === path.resolve(executable)) launches++;
    return Reflect.apply(originalSpawn, this, [command, ...args]);
  };
  const deadline = setTimeout(() => {
    console.error("Public consumer smoke exceeded 30 seconds");
    process.exit(1);
  }, 30000);
  try {
    // The SDK selects its installed platform binary; no executablePath override.
    await smoke(init);
    assert.equal(launches, 1, "Expected one helper for all ten HTTPS requests");
    console.log(
      `PASS: public ${metadata.name}@${metadata.version}, exact alias, clean npm ci, signatures, seven checksums, one helper`
    );
  } finally {
    clearTimeout(deadline);
    childProcess.spawn = originalSpawn;
  }
}

main().catch((error) => {
  console.error(error.stack || error.message);
  process.exitCode = 1;
});
