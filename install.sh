#!/usr/bin/env sh
set -eu

INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"
ZZAM_TIGER_REPO="${ZZAM_TIGER_REPO:-SKAIBlue/zzam-tiger}"

fail() {
  printf 'zzam-tiger installer: %s\n' "$1" >&2
  exit 1
}

command -v go >/dev/null 2>&1 || fail "Go 1.24 or newer is required: https://go.dev/dl/"

GO_VERSION="$(go env GOVERSION | sed 's/^go//')"
GO_MAJOR="$(printf '%s' "$GO_VERSION" | cut -d. -f1)"
GO_MINOR="$(printf '%s' "$GO_VERSION" | cut -d. -f2)"
if [ "$GO_MAJOR" -lt 1 ] || { [ "$GO_MAJOR" -eq 1 ] && [ "$GO_MINOR" -lt 24 ]; }; then
  fail "Go 1.24 or newer is required (found $GO_VERSION)"
fi

mkdir -p "$INSTALL_DIR"
INSTALL_DIR=$(CDPATH= cd "$INSTALL_DIR" && pwd)

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" 2>/dev/null && pwd || true)
if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/go.mod" ]; then
  printf 'Building zzam-tiger from %s…\n' "$SCRIPT_DIR"
  (
    cd "$SCRIPT_DIR"
    go build -buildvcs=false -trimpath -ldflags "-s -w" -o "$INSTALL_DIR/zt" ./cmd/zt
  )
else
  printf 'Installing zzam-tiger from github.com/%s…\n' "$ZZAM_TIGER_REPO"
  GOBIN="$INSTALL_DIR" go install "github.com/$ZZAM_TIGER_REPO/cmd/zt@latest"
fi

chmod +x "$INSTALL_DIR/zt"
printf '\nInstalled: %s/zt\n' "$INSTALL_DIR"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Add this directory to PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac

if ! command -v gh >/dev/null 2>&1 && ! command -v glab >/dev/null 2>&1; then
  printf '\nInstall the CLI for your repository provider before running zt:\n'
  printf '  GitHub: https://cli.github.com/  (then: gh auth login)\n'
  printf '  GitLab: https://gitlab.com/gitlab-org/cli  (then: glab auth login)\n'
fi
