# Repository Agent Instructions

## Installation

- After completing modifications, install the local build as `$HOME/.local/bin/zt-beta` (create `$HOME/.local/bin` if needed). Do not overwrite the regular `zt` installation or install the beta under GOPATH.
- Use `go build -o "$HOME/.local/bin/zt-beta" ./cmd/zt` so the instruction works on every developer machine without hard-coded user paths.

## TUI Modal Usage

Use `Model.OpenModal(ModalRequest{...})` for interactive overlays. `ModalRequest` accepts `HasConfirm` and ordered `ModalItem` values. Supported item types are `select`, `text`, `divider`, `input`, and `button`; `select` options are nested in `Items`. `ID` identifies returned values, `Default` supplies an input/default selection, `Required` validates input/select controls, and `CloseOnClick` makes a button return `true` under its ID.

After a modal closes, call `LastModalResult()` to retrieve `{Confirm, Results}`. `Confirm` is false after Escape/Cancel. Modal input owns keyboard and mouse events while visible; do not add underlying-screen shortcut handling for an open modal.

```go
m, _ = m.OpenModal(tui.ModalRequest{
    HasConfirm: true,
    Items: []tui.ModalItem{
        {Type: "select", ID: "language", Text: "언어 선택", Items: []tui.ModalItem{{ID: "ko", Text: "한국어"}, {ID: "en", Text: "English"}}},
        {Type: "input", ID: "filter", Text: "Filter", Default: "홍", Required: true},
        {Type: "button", ID: "commit", Text: "Commit", CloseOnClick: true},
    },
})
```

## GitHub Issue Management

- Use the GitHub CLI (`gh`) for all issue-management operations, including creating, viewing, searching, editing, commenting on, linking, and closing issues.
- Do not manage issues through ad hoc API calls or assume issue details from memory. Read the current issue state with `gh issue view` before acting on an existing issue.
- Write every issue title, description, checklist, acceptance criterion, and issue comment in English.
- Before creating a new issue, search for an existing or duplicate issue with `gh issue list` or `gh issue search`.
- Keep issue state accurate as work progresses. Add relevant implementation notes, blockers, decisions, verification results, and follow-up work through `gh issue comment` or `gh issue edit`.
- Close an issue with `gh issue close` only after its acceptance criteria have been satisfied and verification evidence has been recorded.

## Issue Quality Standard

Write issues so that another contributor or coding agent can complete the work independently without relying on private conversation history or unstated assumptions.

Every implementation issue must include:

1. **Summary** — a concise description of the requested outcome and why it matters.
2. **Background and context** — relevant product behavior, technical context, prior decisions, and links to related issues, pull requests, files, logs, or documentation.
3. **Current behavior** — what happens now, including reproducible symptoms or limitations when applicable.
4. **Desired behavior** — the observable end state after the issue is completed.
5. **Scope** — the components, modules, or workflows included in the work.
6. **Out of scope** — nearby work that must not be included, especially where scope could be ambiguous.
7. **Implementation guidance** — known constraints, important code locations, existing patterns to reuse, compatibility requirements, and prohibited approaches. Do not prescribe an implementation when the design is intentionally open.
8. **Acceptance criteria** — objective, testable conditions written as a checklist.
9. **Verification plan** — the tests, commands, manual checks, or evidence required to demonstrate completion.
10. **Dependencies and blockers** — prerequisite issues, external decisions, credentials, environments, or data requirements.
11. **Risks and edge cases** — failure modes, compatibility concerns, migration considerations, and boundary conditions that must be handled.

When information is unavailable, explicitly mark it as unknown or requiring investigation instead of omitting it. Avoid vague phrases such as “fix it,” “make it better,” or “handle edge cases” unless they are followed by concrete behavior and measurable acceptance criteria.

## Recommended Issue Body

```markdown
## Summary

## Background and Context

## Current Behavior

## Desired Behavior

## Scope

## Out of Scope

## Implementation Guidance

## Acceptance Criteria

- [ ]

## Verification Plan

## Dependencies and Blockers

## Risks and Edge Cases
```
