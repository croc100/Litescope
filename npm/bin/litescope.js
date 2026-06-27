#!/usr/bin/env node
// Launcher for the litescope binary. Ensures the platform binary is present
// (downloading it on first run if the postinstall step was skipped), then
// forwards all arguments and exit codes transparently.
"use strict";

const { spawn } = require("child_process");
const { ensureBinary } = require("../scripts/lib");

(async () => {
  let bin;
  try {
    bin = await ensureBinary();
  } catch (err) {
    console.error(`litescope: could not obtain the binary: ${err.message}`);
    console.error("Install manually from https://github.com/croc100/Litescope/releases");
    process.exit(1);
  }

  const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });
  child.on("error", (err) => {
    console.error(`litescope: failed to start: ${err.message}`);
    process.exit(1);
  });
  child.on("exit", (code, signal) => {
    if (signal) process.kill(process.pid, signal);
    else process.exit(code === null ? 1 : code);
  });
})();
