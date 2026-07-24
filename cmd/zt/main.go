package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/SKAIBlue/zzam-tiger/internal/provider"
	"github.com/SKAIBlue/zzam-tiger/internal/tui"
	"github.com/SKAIBlue/zzam-tiger/internal/updater"
	"github.com/SKAIBlue/zzam-tiger/internal/worktree"
)

var version = "dev"

func main() {
	providerName := flag.String("provider", "auto", "provider: auto, github, or gitlab")
	repo := flag.String("repo", "", "repository (owner/name or group/project); defaults to the current git repository")
	refresh := flag.Duration("refresh", 5*time.Second, "remote provider refresh interval (0 disables; local tabs use filesystem events)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	runner := provider.ExecRunner{}
	workspace, _ := worktree.Open(".", runner)
	var model tui.Model
	if workspace == nil {
		// Outside a Git repository, zt is a filesystem browser. Remote tabs are
		// intentionally hidden because there is no local repository context.
		model = tui.NewFilesOnly(worktree.NewFilesystem("."))
	} else {
		backend, remoteErr := provider.Detect(*providerName, *repo, runner)
		model = tui.NewWithWorktree(backend, *refresh, workspace)
		if remoteErr != nil {
			if missing, ok := provider.IsMissingCLI(remoteErr); ok {
				remoteErr = fmt.Errorf("%w; %s", remoteErr, missing.InstallGuide())
			}
			model = model.WithRemoteUnavailable(remoteErr)
		}
	}
	model = model.WithUpdates(version, updater.CheckLatest, updater.InstallCommand)
	defer model.Close()
	var root tea.Model = newWheelCoalescingModel(model)
	program := tea.NewProgram(
		root,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithFilter(coalesceWheelInput()),
	)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "zt: %v\n", err)
		os.Exit(1)
	}
}

// wheelInputMsg is an inexpensive representation of one terminal wheel event.
// It is accumulated by wheelCoalescingModel rather than rendering immediately.
type wheelInputMsg struct {
	button tea.MouseButton
	x, y   int
}

type wheelFlushMsg struct{}

// coalesceWheelInput replaces only wheel presses. Other mouse input continues
// through the normal Bubble Tea path unchanged.
func coalesceWheelInput() func(tea.Model, tea.Msg) tea.Msg {
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		mouse, ok := msg.(tea.MouseMsg)
		if !ok || mouse.Action != tea.MouseActionPress || (mouse.Button != tea.MouseButtonWheelUp && mouse.Button != tea.MouseButtonWheelDown) {
			return msg
		}
		return wheelInputMsg{button: mouse.Button, x: mouse.X, y: mouse.Y}
	}
}

// wheelCoalescingModel consumes all wheel events but invokes the wrapped
// model once per frame with their combined movement. This keeps a Kitty wheel
// burst from producing a full View/render for every terminal event while
// preserving the amount and direction of scrolling.
type wheelCoalescingModel struct {
	model        tea.Model
	pending      int
	pendingX     int
	pendingY     int
	flushPending bool
	view         string
	viewValid    bool
}

func newWheelCoalescingModel(model tea.Model) *wheelCoalescingModel {
	return &wheelCoalescingModel{model: model}
}

func (m *wheelCoalescingModel) Init() tea.Cmd {
	return m.model.Init()
}

func (m *wheelCoalescingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case wheelInputMsg:
		delta := 1
		if msg.button == tea.MouseButtonWheelUp {
			delta = -1
		}
		if m.pending != 0 && ((m.pending < 0) != (delta < 0) || m.pendingX != msg.x || m.pendingY != msg.y) {
			m.flushWheel()
		}
		if m.pending == 0 {
			m.pendingX, m.pendingY = msg.x, msg.y
		}
		m.pending += delta
		if m.flushPending {
			return m, nil
		}
		m.flushPending = true
		return m, tea.Tick(time.Second/60, func(time.Time) tea.Msg { return wheelFlushMsg{} })
	case wheelFlushMsg:
		m.flushPending = false
		m.flushWheel()
		return m, nil
	default:
		// Preserve input ordering: a key or a resize observes all wheel input
		// received before it instead of overtaking the pending scroll.
		m.flushWheel()
		var cmd tea.Cmd
		m.model, cmd = m.model.Update(msg)
		m.viewValid = false
		return m, cmd
	}
}

func (m *wheelCoalescingModel) flushWheel() {
	if m.pending == 0 {
		return
	}
	m.model, _ = m.model.Update(tui.WheelScrollMsg{Delta: m.pending, X: m.pendingX, Y: m.pendingY})
	m.pending = 0
	m.viewValid = false
}

func (m *wheelCoalescingModel) View() string {
	if !m.viewValid {
		m.view = m.model.View()
		m.viewValid = true
	}
	return m.view
}
