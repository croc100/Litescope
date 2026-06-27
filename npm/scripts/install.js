// postinstall: fetch the platform binary up front. If it fails (offline,
// air-gapped CI, --ignore-scripts), we don't fail the install — the launcher
// downloads it lazily on first run instead.
"use strict";

const { ensureBinary } = require("./lib");

ensureBinary()
  .then((p) => {
    console.log(`litescope: binary ready (${p})`);
  })
  .catch((err) => {
    console.error(`litescope: postinstall download skipped: ${err.message}`);
    console.error("litescope: the binary will be fetched on first run.");
    // Exit 0 — never break `npm install` over this.
  });
