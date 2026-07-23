#!/usr/bin/env sh
set -eu

INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"
ZZAM_TIGER_REPO="${ZZAM_TIGER_REPO:-SKAIBlue/zzam-tiger}"
GITHUB_API_URL="${GITHUB_API_URL:-https://api.github.com}"
GITHUB_URL="${GITHUB_URL:-https://github.com}"

fail() {
  printf 'zzam-tiger installer: %s\n' "$1" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=macos ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

machine=$(uname -m)
case "$os:$machine" in
  linux:x86_64|linux:amd64) platform=linux_amd64 ;;
  linux:aarch64|linux:arm64) platform=linux_arm64 ;;
  linux:i386|linux:i486|linux:i586|linux:i686|linux:x86) platform=linux_x86 ;;
  macos:arm64|macos:aarch64) platform=macos_apple_silicon ;;
  macos:x86_64|macos:amd64) platform=macos_universal ;;
  *) fail "unsupported platform: $os/$machine" ;;
esac

printf 'Finding the latest zzam-tiger release for %s/%s…\n' "$os" "$machine"
release_json=$(curl -fsSL \
  -H 'Accept: application/vnd.github+json' \
  -H 'X-GitHub-Api-Version: 2022-11-28' \
  "$GITHUB_API_URL/repos/$ZZAM_TIGER_REPO/releases/latest") || \
  fail "could not fetch the latest release from GitHub"

version=$(printf '%s\n' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed -n '1p')
[ -n "$version" ] || fail "the latest GitHub release did not contain a tag"

safe_version=$(printf '%s' "$version" | tr '/' '-')
archive="zt_${safe_version}_${platform}.tar.gz"
download_base="$GITHUB_URL/$ZZAM_TIGER_REPO/releases/download/$version"

tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t zzam-tiger)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

printf 'Downloading zzam-tiger %s (%s)…\n' "$version" "$archive"
curl -fsSL --retry 3 --retry-delay 1 -o "$tmp_dir/$archive" "$download_base/$archive" || \
  fail "release asset not found: $archive"
curl -fsSL --retry 3 --retry-delay 1 -o "$tmp_dir/checksums.txt" "$download_base/checksums.txt" || \
  fail "could not download checksums.txt"

expected=$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1; exit }' "$tmp_dir/checksums.txt")
[ -n "$expected" ] || fail "checksum not found for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp_dir/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1}')
else
  fail "sha256sum or shasum is required to verify the download"
fi
[ "$actual" = "$expected" ] || fail "checksum verification failed for $archive"

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" zt || fail "could not extract $archive"
[ -f "$tmp_dir/zt" ] || fail "the release archive did not contain zt"
chmod +x "$tmp_dir/zt"

mkdir -p "$INSTALL_DIR"
INSTALL_DIR=$(CDPATH= cd "$INSTALL_DIR" && pwd)
mv "$tmp_dir/zt" "$INSTALL_DIR/zt" || fail "could not install to $INSTALL_DIR (check permissions)"

printf '\nInstalled zzam-tiger %s: %s/zt\n' "$version" "$INSTALL_DIR"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Add this directory to PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac

if ! command -v gh >/dev/null 2>&1 && ! command -v glab >/dev/null 2>&1; then
  printf '\nInstall the CLI for your repository provider before running zt:\n'
  printf '  GitHub: https://cli.github.com/  (then: gh auth login)\n'
  printf '  GitLab: https://gitlab.com/gitlab-org/cli  (then: glab auth login)\n'
fi
