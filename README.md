# gtui

`gtui` is a mouse-friendly terminal UI for local Git worktrees plus GitHub and GitLab repositories. It uses the authenticated `gh` or `glab` CLI instead of managing separate API tokens.

The provider is selected from the current repository's `origin` URL. `github.com` uses GitHub; every other host uses GitLab. A GitHub origin requires `gh`, while a GitLab origin requires `glab`. If the required CLI is missing, `gtui` exits before entering the alternate screen and prints platform-specific installation and authentication instructions.

## Features

- `Files` opens first with a lazily loaded, filterable file tree and read-only preview; directories are read asynchronously only when expanded, and Kitty terminals render PNG, JPEG, and GIF images inline, plus SVG when `rsvg-convert` or ImageMagick is available, with a safe fallback otherwise
- `Commit` uses staged/unstaged groups with per-file stage and unstage actions plus a selected-file diff
- Commit diffs use a side-by-side old/new layout when space allows and fall back to a unified patch in narrow terminals
- Remote tabs continue with pull/merge requests, issues, milestones, branches, `Graph` commit history, and GitHub Actions or GitLab pipelines
- CI run details include job logs; selected runs can be cancelled or rerun from the list or detail view
- Status filters with `←`/`→`: Open, Assigned to me, Closed, Merged, and All where applicable; Assigned to me shows only open items
- Keyboard and mouse navigation, clickable tabs/filters/rows, wheel scrolling, and drag-selected diff review ranges
- Issue and PR/MR rows show state and assignees; items assigned to you have highlighted titles
- Full detail views with separately boxed Markdown descriptions, updates, reviews, comments, and actual line-numbered diff content
- Existing inline review threads are shown directly beneath their Diff lines
- Dedicated PR/MR diff browser with file, line, and multi-line range navigation plus inline review comments
- Commit details include the same file/line diff browser, existing commit comments, and new whole-commit or single-line comments
- Click a Diff in PR/MR detail to open it, or drag multiple lines there to review without leaving detail
- Review threads support replies and clickable Resolve actions
- Multiline Markdown comments on issues and PRs/MRs in a bottom-floating composer
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
| Anywhere in a list tab | `1` … `8`, `Shift+1` … `Shift+8`, `Tab`, mouse click | Switch tabs |
| Files / Commit | `/`, then type; `Esc` | Focus the path filter; finish filtering |
| Files | `↑`/`↓` or `j`/`k`, mouse wheel | Select a file or directory and update the read-only preview |
| Files | `Enter` / `→` / `←`, or directory click | Expand or collapse a directory and load its immediate children on demand |
| Commit | `↑`/`↓` or `j`/`k`, mouse wheel or click | Select a staged or unstaged file and update its diff |
| Commit | `Space` | Toggle the selected file between staged and unstaged |
| Commit | `S` / `U` | Stage / unstage the selected file |
| Files / Commit | `PgUp` / `PgDn`, wheel over preview | Scroll the file preview or diff without changing the selected file |
| List | `←`/`→` or `h`/`l` | Change status filter |
| List | `↑`/`↓` or `j`/`k`, mouse wheel | Move selection |
| List | `Enter` or row click | Open details |
| Issue list/detail | `C` / `O` | Close / reopen the selected issue |
| Issue or PR/MR list/detail | `A` / `U` | Assign / unassign yourself |
| Actions/pipeline list/detail | `X` / `R` | Cancel / rerun the selected CI run |
| Detail | `Esc` | Return to the list |
| Detail | `↑`/`↓`, `j`/`k`, mouse wheel, `PgUp`/`PgDn` | Scroll |
| Issue, PR/MR, or commit detail | `N` | Write a multiline Markdown comment |
| PR/MR detail | `R` | Write a top-level review without opening the diff browser |
| PR/MR detail | `D` | Open the dedicated diff browser |
| Commit detail | `D` | Open the commit diff browser |
| PR/MR detail Diff | Click | Open the clicked file and line in the diff browser |
| PR/MR detail Diff | Left-button drag | Select multiple lines and immediately write a review |
| PR/MR detail | `M` | Merge |
| Issue detail | `L` | Set comma-separated labels |
| Diff browser | `←`/`→` or `h`/`l` | Change file |
| Diff browser | `↑`/`↓` or `j`/`k`, mouse wheel | Select a reviewable line |
| Diff browser | Left-button drag | Select a multi-line range and immediately write a review |
| Diff browser | `V` | Start or cancel a multi-line review range |
| Diff browser | `Enter` | Write an inline review on the selected line or range |
| Commit diff browser | `Enter` | Write a comment on the selected line |
| Diff browser/review thread | `P` or thread click | Reply to the selected review thread |
| Diff browser/review thread | `X` or `[Resolve]` click | Resolve the selected review thread |
| Diff browser | `Esc` | Cancel an active range, then return to detail |
| Comment editor | `Ctrl+S` / `Esc` | Submit / cancel |
| List/detail | `r` | Refresh now |

`Shift+1` … `Shift+8` are received by terminals as `!`, `@`, `#`, `$`, `%`, `^`, `&`, and `*`; `gtui` handles those sequences directly.

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/gtui
```
