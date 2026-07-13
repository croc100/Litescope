#!/usr/bin/env bash
#
# Sign, notarize and staple the macOS Litescope.app for distribution.
#
# Prerequisites (you provide these — they are never committed):
#   1. A "Developer ID Application" certificate installed in your login Keychain
#      (Apple Developer Program membership required).
#   2. An app-specific password for notarization, or a notarytool keychain profile.
#
# Required environment variables:
#   DEVELOPER_ID   e.g. "Developer ID Application: Your Name (TEAMID)"
#   AC_USERNAME    your Apple ID email
#   AC_PASSWORD    an app-specific password (appleid.apple.com ▸ Sign-In & Security)
#   AC_TEAM_ID     your 10-char Apple Developer Team ID
#
# Usage (from the gui/ directory):
#   ./build/darwin/sign-and-notarize.sh
#
set -euo pipefail

: "${DEVELOPER_ID:?set DEVELOPER_ID to your 'Developer ID Application: …' identity}"
: "${AC_USERNAME:?set AC_USERNAME to your Apple ID}"
: "${AC_PASSWORD:?set AC_PASSWORD to an app-specific password}"
: "${AC_TEAM_ID:?set AC_TEAM_ID to your Team ID}"

APP="build/bin/Litescope.app"
ZIP="build/bin/Litescope.zip"
ENTITLEMENTS="build/darwin/entitlements.plist"

echo "▸ Building universal binary…"
wails build -platform darwin/universal -clean

echo "▸ Signing with hardened runtime…"
codesign --force --deep --options runtime --timestamp \
  --entitlements "$ENTITLEMENTS" \
  --sign "$DEVELOPER_ID" "$APP"

echo "▸ Verifying signature…"
codesign --verify --deep --strict --verbose=2 "$APP"

echo "▸ Zipping for notarization…"
/usr/bin/ditto -c -k --keepParent "$APP" "$ZIP"

echo "▸ Submitting to Apple notary service (this can take a few minutes)…"
xcrun notarytool submit "$ZIP" \
  --apple-id "$AC_USERNAME" \
  --password "$AC_PASSWORD" \
  --team-id "$AC_TEAM_ID" \
  --wait

echo "▸ Stapling the notarization ticket…"
xcrun stapler staple "$APP"

echo "▸ Final Gatekeeper check…"
spctl --assess --type execute --verbose=4 "$APP"

echo "✓ Done — $APP is signed, notarized and stapled."
