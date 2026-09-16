const { execSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const ROOT = path.resolve(__dirname, "..", "..");
const EXT_DIR = path.resolve(__dirname, "..");
const BIN_DIR = path.join(EXT_DIR, "bin");

const targets = [
  { goos: "darwin", goarch: "amd64", vscodeTarget: "darwin-x64" },
  { goos: "darwin", goarch: "arm64", vscodeTarget: "darwin-arm64" },
  { goos: "linux", goarch: "amd64", vscodeTarget: "linux-x64" },
  { goos: "linux", goarch: "arm64", vscodeTarget: "linux-arm64" },
];

function parseSemver(tag) {
  const m = /^v(\d+)\.(\d+)\.(\d+)$/.exec(tag);
  if (!m) return null;
  return { tag, parts: [Number(m[1]), Number(m[2]), Number(m[3])] };
}

function compareSemverDesc(a, b) {
  for (let i = 0; i < 3; i++) {
    if (a.parts[i] !== b.parts[i]) return b.parts[i] - a.parts[i];
  }
  return 0;
}

function resolveReleaseTag() {
  const refTag = process.env.GITHUB_REF_NAME;
  if (parseSemver(refTag || "")) {
    return refTag;
  }

  try {
    const tags = execSync("git tag --points-at HEAD", {
      cwd: ROOT,
      encoding: "utf-8",
    })
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean)
      .map(parseSemver)
      .filter(Boolean)
      .sort(compareSemverDesc);
    if (tags.length > 0) {
      return tags[0].tag;
    }
  } catch {
    // Fall through to package.json version.
  }

  return null;
}

// Sync version from release tag (v1.2.3 → 1.2.3) so package.json never drifts.
const tag = resolveReleaseTag();
if (tag) {
  const version = tag.replace(/^v/, "");
  const pkgPath = path.join(EXT_DIR, "package.json");
  const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf-8"));
  if (pkg.version !== version) {
    console.log(`Updating package.json version: ${pkg.version} → ${version}`);
    pkg.version = version;
    fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
  }
} else {
  console.log("No release tag found, using version from package.json.");
}

// Copy README from repo root so vsce includes it in the .vsix.
const readmeSrc = path.join(ROOT, "README.md");
const readmeDst = path.join(EXT_DIR, "README.md");
if (fs.existsSync(readmeSrc)) {
  fs.copyFileSync(readmeSrc, readmeDst);
}

// Allow building a single target via env var.
const only = process.env.VSCE_TARGET;

for (const t of targets) {
  if (only && t.vscodeTarget !== only) {
    continue;
  }

  console.log(`\n=== ${t.vscodeTarget} (${t.goos}/${t.goarch}) ===`);

  // Clean and create bin dir.
  if (fs.existsSync(BIN_DIR)) {
    fs.rmSync(BIN_DIR, { recursive: true });
  }
  fs.mkdirSync(BIN_DIR, { recursive: true });

  const binaryName = "make-ls";
  const outPath = path.join(BIN_DIR, binaryName);

  // Cross-compile.
  console.log(`Compiling ${t.goos}/${t.goarch}...`);
  execSync(
    `GOOS=${t.goos} GOARCH=${t.goarch} CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ${outPath} ./cmd/make-ls`,
    { cwd: ROOT, stdio: "inherit" },
  );

  // Make executable.
  fs.chmodSync(outPath, 0o755);

  // Package .vsix.
  console.log(`Packaging ${t.vscodeTarget}...`);
  execSync(`npx vsce package --target ${t.vscodeTarget}`, {
    cwd: EXT_DIR,
    stdio: "inherit",
  });
}

// Clean up build artifacts.
if (fs.existsSync(BIN_DIR)) {
  fs.rmSync(BIN_DIR, { recursive: true });
}
if (fs.existsSync(readmeDst)) {
  fs.unlinkSync(readmeDst);
}

console.log("\nDone. .vsix files:");
const vsixFiles = fs.readdirSync(EXT_DIR).filter((f) => f.endsWith(".vsix"));
for (const f of vsixFiles) {
  const stat = fs.statSync(path.join(EXT_DIR, f));
  console.log(`  ${f} (${(stat.size / 1024 / 1024).toFixed(1)} MB)`);
}

const PUBLISH_TIMEOUT_MS = 180_000;
const RETRY_DELAYS_MS = [5_000, 20_000];
const failures = [];

function sleep(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

// A timeout is self-inflicted, not evidence the network is flaky: retrying one
// multiplies the wait instead of recovering from it.
function isTransient(output) {
  return (
    /request timeout|econnreset|socket hang up|getaddrinfo|50\d\b/i.test(
      output,
    ) && !/etimedout/i.test(output)
  );
}

// One call per .vsix: the marketplace stalls on a rotating target roughly once
// a release, and per-file timing in the log is the only evidence of which.
function publish(registry, file, cmd, env) {
  const started = Date.now();
  for (let attempt = 0; ; attempt++) {
    try {
      process.stdout.write(
        execSync(cmd, {
          cwd: EXT_DIR,
          stdio: ["ignore", "pipe", "pipe"],
          encoding: "utf-8",
          timeout: PUBLISH_TIMEOUT_MS,
          env: { ...process.env, ...env },
        }) || "",
      );
      console.log(`  ${file} → ${registry} (${elapsed(started)})`);
      return;
    } catch (err) {
      const output = `${err.stdout || ""}${err.stderr || ""}${err.message || ""}`;
      process.stdout.write(output);
      if (attempt >= RETRY_DELAYS_MS.length || !isTransient(output)) {
        console.error(
          `  FAILED after ${elapsed(started)}: ${file} → ${registry}`,
        );
        failures.push(`${file} → ${registry}`);
        return;
      }
      const delay = RETRY_DELAYS_MS[attempt];
      console.error(`    transient error, retrying in ${delay / 1000}s`);
      sleep(delay);
    }
  }
}

function elapsed(since) {
  return `${((Date.now() - since) / 1000).toFixed(1)}s`;
}

function publishEach(registry, tokenEnv, build) {
  if (!process.env[tokenEnv]) {
    console.log(`\nSkipping ${registry} publish (no ${tokenEnv}).`);
    return;
  }
  console.log(`\nPublishing to ${registry}...`);
  for (const f of vsixFiles) {
    const { cmd, env } = build(path.join(EXT_DIR, f));
    publish(registry, f, cmd, env);
  }
}

publishEach("VS Code Marketplace", "VSCODE_PUBLISH_TOKEN", (vsixPath) => ({
  cmd: `npx vsce publish --skip-duplicate --packagePath ${vsixPath}`,
  env: { VSCE_PAT: process.env.VSCODE_PUBLISH_TOKEN },
}));

publishEach("Open VSX", "OPVSX_PUBLISH_TOKEN", (vsixPath) => ({
  cmd: `npx ovsx publish --skip-duplicate --packagePath ${vsixPath}`,
  env: { OVSX_PAT: process.env.OPVSX_PUBLISH_TOKEN },
}));

if (failures.length > 0) {
  console.error(`\n${failures.length} publish(es) failed:`);
  for (const f of failures) {
    console.error(`  ${f}`);
  }
  process.exit(1);
}
