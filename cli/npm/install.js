// Downloads the reevit release binary matching this package's version from
// GitHub Releases. Dependency-free: native gunzip + a minimal tar reader
// (releases ship tar.gz on every platform for exactly this reason).
"use strict";

const fs = require("fs");
const path = require("path");
const zlib = require("zlib");
const { version } = require("./package.json");

const REPO = "Reevit-Platform/cli";

const OS = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
const ARCH = { x64: "amd64", arm64: "arm64" }[process.arch];

if (!OS || !ARCH) {
  console.error(`reevit: unsupported platform ${process.platform}/${process.arch} — download a binary from https://github.com/${REPO}/releases`);
  process.exit(1);
}

const asset = `reevit_${version}_${OS}_${ARCH}.tar.gz`;
const url = `https://github.com/${REPO}/releases/download/v${version}/${asset}`;
const binName = OS === "windows" ? "reevit.exe" : "reevit";
const dest = path.join(__dirname, "bin", binName);

async function download(u, redirects = 0) {
  if (redirects > 5) throw new Error("too many redirects");
  const res = await fetch(u, { redirect: "follow" });
  if (!res.ok) throw new Error(`GET ${u} → ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}

// Minimal ustar reader: 512-byte headers, name at 0..100, size (octal) at
// 124..136, content padded to 512. Enough for goreleaser archives.
function extract(tarBuf, wantName) {
  let off = 0;
  while (off + 512 <= tarBuf.length) {
    const name = tarBuf.subarray(off, off + 100).toString().replace(/\0.*$/, "");
    if (!name) break;
    const size = parseInt(tarBuf.subarray(off + 124, off + 136).toString().replace(/\0.*$/, ""), 8) || 0;
    const body = tarBuf.subarray(off + 512, off + 512 + size);
    if (name === wantName || name.endsWith("/" + wantName)) return body;
    off += 512 + Math.ceil(size / 512) * 512;
  }
  throw new Error(`${wantName} not found in archive`);
}

(async () => {
  const gz = await download(url);
  const bin = extract(zlib.gunzipSync(gz), binName);
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(dest, bin, { mode: 0o755 });
  console.log(`reevit ${version} installed for ${OS}/${ARCH}`);
})().catch((err) => {
  console.error(`reevit: install failed: ${err.message}`);
  console.error(`You can download a binary directly from https://github.com/${REPO}/releases/tag/v${version}`);
  process.exit(1);
});
