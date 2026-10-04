#!/usr/bin/env node
"use strict";

// Execute the same unpublished archives produced by prepare-release.cjs.
// No cross-platform emulation: a mismatched Node or Go host fails the job.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const http = require("node:http");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");

const run = promisify(execFile);
const root = path.resolve(__dirname, "..");
const [artifacts, platform, arch] = process.argv.slice(2);
assert.ok(artifacts && platform && arch, "Pass the prepared artifact directory, native platform and architecture");
assert.equal(process.platform, platform, "The smoke test must run on the advertised operating system");
assert.equal(process.arch, arch, "The smoke test must run with native Node, not architecture emulation");
const goos = { darwin: "darwin", linux: "linux", win32: "windows" }[platform];
const goarch = { x64: "amd64", arm64: "arm64" }[arch];
assert.ok(goos && goarch, "Unsupported native platform");

async function main() {
  const evidence = JSON.parse(fs.readFileSync(path.join(artifacts, "release-evidence.json"), "utf8"));
  const sourceCommit = (await run("git", ["rev-parse", "HEAD"], { cwd: root })).stdout.trim();
  assert.equal(evidence.sourceCommit, sourceCommit, "The archives must belong to this exact checkout");
  assert.equal(evidence.sourceDirty, false, "Release archives must be built from a clean checkout");
  assert.equal(evidence.version, require("../npm/package.json").version);
  const host = JSON.parse((await run("go", ["env", "-json", "GOHOSTOS", "GOHOSTARCH"])).stdout);
  assert.deepEqual(host, { GOHOSTOS: goos, GOHOSTARCH: goarch }, "Go must execute generated handlers natively");

  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "reevit-native-release-"));
  await run("tar", ["-xzf", path.join(artifacts, evidence.npmArchive), "-C", fixture]);
  const packageDir = path.join(fixture, "package");
  const install = path.join(packageDir, "install.js");
  const asset = `reevit_${evidence.version}_${goos}_${goarch}.tar.gz`;
  const requested = [];
  await vm.runInNewContext(fs.readFileSync(install, "utf8"), {
    require: createRequire(install), __dirname: packageDir, Buffer, console,
    process: { platform: process.platform, arch: process.arch,
      exit(code) { throw new Error(`Installer exited ${code}`); } },
    async fetch(url) {
      const prefix = `https://github.com/Reevit-Platform/cli/releases/download/v${evidence.version}/`;
      assert.ok(url.startsWith(prefix), `Unexpected release URL: ${url}`);
      const name = url.slice(prefix.length);
      assert.ok(name === asset || name === "checksums.txt", `Unexpected asset: ${name}`);
      requested.push(name);
      const bytes = fs.readFileSync(path.join(artifacts, "dist", name));
      return { ok: true, async arrayBuffer() {
        return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
      } };
    },
  }, { filename: install });
  assert.deepEqual(requested.sort(), [asset, "checksums.txt"].sort());

  const shim = path.join(packageDir, "bin", "reevit.js");
  const project = path.join(fixture, "shop");
  fs.mkdirSync(project);
  fs.writeFileSync(path.join(project, "go.mod"), "module example.test/native-release\n\ngo 1.24.2\n");
  fs.writeFileSync(path.join(project, "main.go"), "package main\n\nfunc main() {}\n");
  const env = { ...process.env, REEVIT_CONFIG: path.join(fixture, "config.json"),
    REEVIT_API_KEY: "pfk_test_native.secret", REEVIT_TELEMETRY: "0", GOOS: goos, GOARCH: goarch };
  const cli = args => run(process.execPath, [shim, ...args], { cwd: project, env, timeout: 60000 });
  const runtimeVersion = (await cli(["--version"])).stdout.trim();
  assert.equal(runtimeVersion, `reevit version ${evidence.version}`);
  const help = (await cli(["--help"])).stdout;
  for (const command of ["init", "listen", "doctor"]) assert.ok(help.includes(command));

  // Bootstrap is the only API dependency of the webhook-only scaffold.
  // Keep it on loopback, then run the generated Go handler with real requests.
  let bootstraps = 0;
  const server = http.createServer(async (req, res) => {
    res.setHeader("Content-Type", "application/json");
    if (req.method === "GET" && req.url === "/v1/cli/account") {
      res.end(JSON.stringify({ organization_id: "org_native", organization_name: "Native smoke" }));
    } else if (req.method === "POST" && req.url === "/v1/cli/bootstrap") {
      let body = "";
      for await (const chunk of req) body += chunk;
      const request = JSON.parse(body);
      bootstraps++;
      res.end(JSON.stringify({ project: { id: request.project_id, organization_id: "org_native" },
        mode: "test", credentials: {}, simulator: { ready: true } }));
    } else {
      res.statusCode = 404;
      res.end(JSON.stringify({ error: "Unexpected native smoke request" }));
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  env.REEVIT_API_URL = `http://127.0.0.1:${server.address().port}`;
  try {
    const firstRun = await cli(["init", "--yes", "--target", "webhook"]);
    fs.writeFileSync(path.join(fixture, "init-output.txt"), firstRun.stdout + firstRun.stderr);
    const generated = path.join(project, "reevit_webhook.go");
    const source = fs.readFileSync(generated, "utf8");
    const projectEnv = fs.readFileSync(path.join(project, ".env"), "utf8");
    await cli(["init", "--yes", "--target", "webhook"]);
    assert.equal(bootstraps, 2, "Both initial setup and idempotent rerun must reach bootstrap");
    assert.equal(fs.readFileSync(generated, "utf8"), source, "Rerun must preserve generated handler");
    assert.equal(fs.readFileSync(path.join(project, ".env"), "utf8"), projectEnv, "Rerun must preserve project values");
    const manifest = JSON.parse(fs.readFileSync(path.join(project, ".reevit", "manifest.json")));
    assert.equal(manifest.status, "complete");
    assert.equal(manifest.cli_version, evidence.version);

    let instrumented = source;
    for (const [anchor, effect] of [
      ["// TODO: fulfil the order for event.Data", 'nativeObserved = append(nativeObserved, "succeeded")'],
      ["// TODO: notify the customer / retry logic", 'nativeObserved = append(nativeObserved, "failed")'],
    ]) {
      assert.ok(instrumented.includes(anchor), `Generated handler no longer has ${anchor}`);
      instrumented = instrumented.replace(anchor, effect);
    }
    fs.writeFileSync(generated, instrumented);
    fs.copyFileSync(path.join(root, "scripts", "testdata", "native-webhook-test.go"),
      path.join(project, "native_webhook_test.go"));
    const contract = await run("go", ["test", "./...", "-count=1", "-v"], { cwd: project, env, timeout: 120000 });
    console.log(contract.stdout);
    fs.writeFileSync(path.join(artifacts, `native-evidence-${platform}-${arch}.json`), JSON.stringify({
      sourceCommit, version: evidence.version, platform, arch, asset,
      archiveSHA256: evidence.platforms.find(item => item.platform === platform && item.arch === arch).sha256,
      runtimeVersion, npmShimHelp: true, bootstrapRuns: bootstraps, idempotentRerun: true,
      generatedHandler: "payment.updated succeeded/failed/pending and legacy type fallback", fixture,
    }, null, 2) + "\n");
    console.log(`Native release smoke passed for ${platform}/${arch}: ${fixture}`);
  } finally {
    await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
