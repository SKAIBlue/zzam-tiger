# gtui

`gtui` is a mouse-friendly terminal UI for GitHub and GitLab repositories. It uses the authenticated `gh` or `glab` CLI instead of managing separate API tokens.

The provider is selected from the current repository's `origin` URL. `github.com` uses GitHub; every other host uses GitLab. A GitHub origin requires `gh`, while a GitLab origin requires `glab`. If the required CLI is missing, `gtui` exits before entering the alternate screen and prints platform-specific installation and authentication instructions.

## Features

- Tabs for pull/merge requests, issues, milestones, branches, and commit history
- Status filters with `←`/`→`: Open, Assigned to me, Closed, Merged, and All where applicable; assigned items keep open entries ahead of closed or merged entries
- Keyboard and mouse navigation, clickable tabs/filters/rows, and wheel scrolling
- Issue and PR/MR rows show state and assignees; items assigned to you have highlighted titles
- Full detail views with separately boxed Markdown descriptions, updates, reviews, and comments
- Merge from a PR/MR detail with `M`
- Assign/unassign yourself on an issue or PR/MR with `A`/`U` from either the list or detail
- Close/reopen an issue with `C`/`O` from either the list or detail, and replace labels with `L` in detail
- Long titles and other text end in `...` when the terminal is too narrow
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
--provider auto|github|gitlab  Override the provider inferred from origin
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
| Issue list/detail | `C` / `O` | Close / reopen the selected issue |
| Issue or PR/MR list/detail | `A` / `U` | Assign / unassign yourself |
| Detail | `Esc` | Return to the list |
| Detail | `↑`/`↓`, `j`/`k`, mouse wheel, `PgUp`/`PgDn` | Scroll |
| PR/MR detail | `M` | Merge |
| Issue detail | `L` | Set comma-separated labels |
| List/detail | `r` | Refresh now |

`Shift+1` … `Shift+5` are received by terminals as `!`, `@`, `#`, `$`, and `%`; `gtui` handles those sequences directly.

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/gtui
```
