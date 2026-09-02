#!/usr/bin/env node
// Thin shim: exec the platform binary fetched by install.js.
"use strict";

const os = require("os");
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

// A child killed by a signal reports status: null. Exiting 0 there made a
// timeout-killed `reevit doctor` look green in CI, so re-raise the signal on
// ourselves (the honest thing: the parent shell sees the same death) and fall
// back to the conventional 128+n if the signal did not take us down.
if (result.signal) {
  process.kill(process.pid, result.signal);
  process.exit(128 + (os.constants.signals[result.signal] || 0));
}

// No status and no signal is a failure, not a success.
process.exit(result.status ?? 1);
