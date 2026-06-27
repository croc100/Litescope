// Shared install logic: resolve the platform's release asset, download it from
// GitHub Releases, and extract the binary. Zero runtime dependencies — archive
// extraction shells out to tar / unzip / Expand-Archive, which exist on every
// supported platform.
"use strict";

const fs = require("fs");
const path = require("path");
const https = require("https");
const { execFileSync } = require("child_process");
const { version } = require("../package.json");

const REPO = "croc100/Litescope";

// platformAsset maps the current Node platform/arch to a release asset name.
function platformAsset() {
  const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!goarch) throw new Error(`unsupported CPU architecture: ${process.arch}`);

  let goos, ext, binName;
  switch (process.platform) {
    case "darwin":
      goos = "darwin"; ext = "tar.gz"; binName = "litescope"; break;
    case "linux":
      goos = "linux"; ext = "tar.gz"; binName = "litescope"; break;
    case "win32":
      goos = "windows"; ext = "zip"; binName = "litescope.exe";
      if (goarch !== "amd64") throw new Error("Windows is only supported on amd64");
      break;
    default:
      throw new Error(`unsupported platform: ${process.platform}`);
  }
  const asset = `litescope_${version}_${goos}_${goarch}.${ext}`;
  return { asset, ext, binName };
}

function vendorDir() {
  return path.join(__dirname, "..", "vendor");
}

// binaryPath returns where the binary lives (it may not exist yet).
function binaryPath() {
  const { binName } = platformAsset();
  return path.join(vendorDir(), binName);
}

// ensureBinary returns the path to the litescope binary, downloading and
// extracting it on first use.
async function ensureBinary() {
  const { asset, ext, binName } = platformAsset();
  const dir = vendorDir();
  const binPath = path.join(dir, binName);
  if (fs.existsSync(binPath)) return binPath;

  fs.mkdirSync(dir, { recursive: true });
  const url = `https://github.com/${REPO}/releases/download/v${version}/${asset}`;
  const archivePath = path.join(dir, asset);

  await download(url, archivePath);
  extract(archivePath, dir, ext);
  fs.unlinkSync(archivePath);
  if (process.platform !== "win32") fs.chmodSync(binPath, 0o755);
  if (!fs.existsSync(binPath)) {
    throw new Error(`archive did not contain ${binName}`);
  }
  return binPath;
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const get = (u, redirects) => {
      if (redirects > 6) return reject(new Error("too many redirects"));
      https
        .get(u, { headers: { "User-Agent": "litescope-npm" } }, (res) => {
          if (
            res.statusCode >= 300 &&
            res.statusCode < 400 &&
            res.headers.location
          ) {
            res.resume();
            return get(res.headers.location, redirects + 1);
          }
          if (res.statusCode !== 200) {
            res.resume();
            return reject(new Error(`download failed (HTTP ${res.statusCode}) for ${u}`));
          }
          const file = fs.createWriteStream(dest);
          res.pipe(file);
          file.on("finish", () => file.close(() => resolve()));
          file.on("error", reject);
        })
        .on("error", reject);
    };
    get(url, 0);
  });
}

function extract(archive, dir, ext) {
  if (ext === "zip") {
    if (process.platform === "win32") {
      execFileSync(
        "powershell",
        ["-NoProfile", "-Command", `Expand-Archive -Force -Path "${archive}" -DestinationPath "${dir}"`],
        { stdio: "ignore" }
      );
    } else {
      execFileSync("unzip", ["-o", archive, "-d", dir], { stdio: "ignore" });
    }
  } else {
    execFileSync("tar", ["-xzf", archive, "-C", dir], { stdio: "ignore" });
  }
}

module.exports = { ensureBinary, binaryPath, platformAsset, version };
