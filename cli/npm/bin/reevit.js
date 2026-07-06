#!/usr/bin/env node
// Thin shim: exec the platform binary fetched by install.js.
"use strict";

const path = require("path");
const { spawnSync } = require("child_process");

const binName = process.platform === "win32" ? "reevit.exe" : "reevit";
const bin = path.join(__dirname, binName);

const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });

if (result.error) {
  if (result.error.code === "ENOENT") {
    console.error("reevit: binary missing — reinstall with `npm install -g @reevit/cli` (postinstall downloads it)");
  } else {
    console.error(`reevit: ${result.error.message}`);
  }
  process.exit(1);
}

process.exit(result.status ?? 0);
