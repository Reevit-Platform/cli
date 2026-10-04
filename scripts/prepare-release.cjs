#!/usr/bin/env node
"use strict";

// Build the actual GoReleaser archives and exercise the packed npm installer
// against local assets. Snapshot mode never publishes or creates a Git tag.
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const { createRequire } = require("node:module");
const { execFileSync } = require("node:child_process");

const root = path.resolve(__dirname, "..");
const output = process.argv[2] && path.resolve(process.argv[2]);
if (!output || fs.existsSync(output)) {
  throw new Error("Pass a new, nonexistent artifact directory; existing paths are never replaced");
}
const { version, os: supportedOS, cpu: supportedCPU } = require("../npm/package.json");
assert.match(version, /^\d+\.\d+\.\d+$/, "npm release version must be stable semver");
fs.mkdirSync(output, { recursive: true });
const config = path.join(output, "goreleaser-local.yaml");
fs.writeFileSync(config, fs.readFileSync(path.join(root, ".goreleaser.yaml"), "utf8") +
  `\ndist: ${JSON.stringify(path.join(output, "dist"))}\nsnapshot:\n  version_template: ${JSON.stringify(version)}\n`);
execFileSync("goreleaser", ["release", "--snapshot", "--skip=homebrew", "--config", config], {
  cwd: root, stdio: "inherit", env: process.env,
});
const packed = JSON.parse(execFileSync("npm", ["pack", "--ignore-scripts", "--json", "--pack-destination", output], {
  cwd: path.join(root, "npm"), encoding: "utf8", env: process.env,
}));
const metadata = Array.isArray(packed) ? packed[0] : packed;
const npmArchive = path.join(output, metadata.filename);
const checksums = fs.readFileSync(path.join(output, "dist", "checksums.txt"));
const osMap = { darwin: "darwin", linux: "linux", win32: "windows" };
const archMap = { x64: "amd64", arm64: "arm64" };

async function main() {
  const evidence = { version, mode: "local-snapshot-unpublished", sourceCommit:
    execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim(),
    sourceDirty: execFileSync("git", ["status", "--porcelain"], { cwd: root, encoding: "utf8" }).trim() !== "",
    npmArchive: metadata.filename, npmIntegrity: metadata.integrity, platforms: [] };
  for (const platform of supportedOS) {
    for (const arch of supportedCPU) {
      const goos = osMap[platform];
      const goarch = archMap[arch];
      assert.ok(goos && goarch, `Unknown advertised platform ${platform}/${arch}`);
      const asset = `reevit_${version}_${goos}_${goarch}.tar.gz`;
      const archive = fs.readFileSync(path.join(output, "dist", asset));
      const fixture = path.join(output, "consumers", `${platform}-${arch}`);
      fs.mkdirSync(fixture, { recursive: true });
      execFileSync("tar", ["-xzf", npmArchive, "-C", fixture]);
      const install = path.join(fixture, "package", "install.js");
      const requested = [];
      const promise = vm.runInNewContext(fs.readFileSync(install, "utf8"), {
        require: createRequire(install), __dirname: path.dirname(install), Buffer,
        console, process: { platform, arch, exit(code) { throw new Error(`Installer exited ${code}`); } },
        async fetch(url) {
          const prefix = `https://github.com/Reevit-Platform/cli/releases/download/v${version}/`;
          assert.ok(url.startsWith(prefix), `Unexpected release URL: ${url}`);
          const name = url.slice(prefix.length);
          requested.push(name);
          const bytes = name === asset ? archive : name === "checksums.txt" ? checksums : null;
          assert.ok(bytes, `Unexpected release asset requested: ${name}`);
          return { ok: true, status: 200, async arrayBuffer() {
            return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
          } };
        },
      }, { filename: install });
      await promise;
      assert.deepEqual(requested.sort(), [asset, "checksums.txt"].sort());
      const binary = path.join(fixture, "package", "bin", goos === "windows" ? "reevit.exe" : "reevit");
      const buildInfo = execFileSync("go", ["version", "-m", binary], { encoding: "utf8" });
      assert.ok(buildInfo.includes(`GOOS=${goos}`) && buildInfo.includes(`GOARCH=${goarch}`), buildInfo);
      let runtimeVersion = null;
      if (platform === process.platform && arch === process.arch) {
        runtimeVersion = execFileSync(binary, ["--version"], { encoding: "utf8" }).trim();
        assert.equal(runtimeVersion, `reevit version ${version}`);
      }
      evidence.platforms.push({ platform, arch, asset,
        sha256: crypto.createHash("sha256").update(archive).digest("hex"), runtimeVersion });
    }
  }
  fs.writeFileSync(path.join(output, "release-evidence.json"), JSON.stringify(evidence, null, 2) + "\n");
  console.log(`Verified packed npm installation for ${evidence.platforms.length} platform archives; artifacts: ${output}`);
}
main().catch(error => { console.error(error); process.exitCode = 1; });
