# gtui

`gtui` is a mouse-friendly terminal UI for GitHub and GitLab repositories. It uses the authenticated `gh` or `glab` CLI instead of managing separate API tokens.

The provider is selected from the current repository's `origin` URL. A GitHub origin requires `gh`; a GitLab origin requires `glab`. If the required CLI is missing, `gtui` exits before entering the alternate screen and prints platform-specific installation and authentication instructions.

## Features

- Tabs for pull/merge requests, issues, milestones, branches, and commit history
- Status filters with `←`/`→`: Open, Closed, Merged, and All where applicable
- Keyboard and mouse navigation, clickable tabs/filters/rows, and wheel scrolling
- Full detail views with separately boxed Markdown descriptions, updates, reviews, and comments
- Merge from a PR/MR detail with `M`
- Close/reopen an issue with `C`/`O`, and replace labels with `L`
- Active-view refresh every 5 seconds by default
- A single Go binary for Linux and macOS

## Prerequisites

- Go 1.24+ when installing from source
- GitHub: [`gh`](https://cli.github.com/) and `gh auth login`
- GitLab: [`glab`](https://gitlab.com/gitlab-org/cli) and `glab auth login`

## Install

From a checkout:

```sh
./install.sh
```

The default destination is `~/.local/bin`. Override it when needed:

```sh
INSTALL_DIR=/usr/local/bin ./install.sh
```

The same script can be piped from a published repository. Set `GTUI_REPO=owner/repo` when using a fork.

## Run

Run inside a cloned GitHub or GitLab repository:

```sh
gtui
```

Useful options:

```text
--provider auto|github|gitlab  Provider override for an unrecognized self-hosted origin
--repo owner/name             Repository override; provider still comes from origin unless set explicitly
--refresh 5s                  Refresh interval; 0 disables automatic refresh
--version                     Print version
```

## Keys and mouse

| Context | Input | Action |
| --- | --- | --- |
| Anywhere | `Ctrl+C` | Quit |
| List | `Shift+1` … `Shift+5`, `Tab`, mouse click | Switch tabs |
| List | `←`/`→` or `h`/`l` | Change status filter |
| List | `↑`/`↓` or `j`/`k`, mouse wheel | Move selection |
| List | `Enter` or row click | Open details |
| Detail | `Esc` | Return to the list |
| Detail | `↑`/`↓`, `j`/`k`, mouse wheel, `PgUp`/`PgDn` | Scroll |
| PR/MR detail | `M` | Merge |
| Issue detail | `C` / `O` | Close / reopen |
| Issue detail | `L` | Set comma-separated labels |
| List/detail | `r` | Refresh now |

`Shift+1` … `Shift+5` are received by terminals as `!`, `@`, `#`, `$`, and `%`; `gtui` handles those sequences directly.

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/gtui
```
