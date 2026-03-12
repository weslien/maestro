package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/weslien/maestro/internal/gsdstate"
	"github.com/weslien/maestro/internal/orchestrator"
	"github.com/weslien/maestro/internal/tracker"
	"github.com/weslien/maestro/internal/tui/components"
	"github.com/weslien/maestro/internal/tui/views"
)

const (
	tabDashboard = 0
	tabIssues    = 1
	tabActions   = 2
)

// App is the top-level Bubbletea model with multi-view support
type App struct {
	orch      *orchestrator.Orchestrator
	gsd       *gsdstate.Provider
	activeTab int
	dashboard *views.DashboardView
	issues    *views.IssuesView
	actions   *views.ActionsView

	// Cached board state from tracker polls
	boardIssues []tracker.Issue
	tickCount   int // counts 1s ticks for periodic board polls

	width    int
	height   int
	quitting bool
}

// NewApp creates a new multi-view TUI application
func NewApp(orch *orchestrator.Orchestrator, gsd *gsdstate.Provider) *App {
	return &App{
		orch:      orch,
		gsd:       gsd,
		activeTab: tabDashboard,
		dashboard: views.NewDashboardView(orch),
		issues:    views.NewIssuesView(orch),
		actions:   views.NewActionsView(orch),
		width:     120,
		height:    40,
	}
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.listenEvents(),
		a.tick(),
		a.fetchGSDState(),
		a.pollBoard(),
	)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			a.quitting = true
			a.orch.Stop()
			return a, tea.Quit
		case "1":
			a.activeTab = tabDashboard
			return a, nil
		case "2":
			a.activeTab = tabIssues
			return a, nil
		case "3":
			a.activeTab = tabActions
			return a, nil
		case "r":
			a.issues.RefreshIssues()
			return a, tea.Batch(a.fetchGSDState(), a.pollBoard())
		default:
			return a.delegateToView(msg)
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		viewHeight := a.height - 4
		a.dashboard.SetSize(a.width, viewHeight)
		a.issues.SetSize(a.width, viewHeight)
		a.actions.SetSize(a.width, viewHeight)
		return a, nil

	case eventMsg:
		event := orchestrator.Event(msg)
		a.issues.HandleEvent(event)
		a.issues.RefreshIssues()
		a.dashboard.Refresh(a.boardIssues)
		a.actions.Refresh(a.boardIssues)
		return a, a.listenEvents()

	case tickMsg:
		a.issues.RefreshIssues()
		a.dashboard.Refresh(a.boardIssues)
		a.actions.Refresh(a.boardIssues)
		a.tickCount++
		// Re-poll board every 30s
		if a.tickCount%30 == 0 {
			return a, tea.Batch(a.tick(), a.pollBoard())
		}
		return a, a.tick()

	case gsdStateMsg:
		a.dashboard.RefreshGSD(msg.state, msg.err)
		a.dashboard.Refresh(a.boardIssues)
		return a, nil

	case boardPollMsg:
		if msg.err == nil {
			a.boardIssues = msg.issues
			a.dashboard.Refresh(a.boardIssues)
			a.actions.Refresh(a.boardIssues)
		}
		return a, nil

	case views.RefreshMsg:
		a.issues.RefreshIssues()
		return a, tea.Batch(a.fetchGSDState(), a.pollBoard())
	}

	// Pass unhandled messages to active view (e.g., actionResultMsg)
	return a.delegateToView(msg)
}

func (a *App) delegateToView(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch a.activeTab {
	case tabDashboard:
		_, cmd = a.dashboard.Update(msg)
	case tabIssues:
		_, cmd = a.issues.Update(msg)
	case tabActions:
		_, cmd = a.actions.Update(msg)
	}

	return a, cmd
}

func (a *App) View() string {
	if a.quitting {
		return "Shutting down maestro...\n"
	}

	header := components.RenderHeader(a.activeTab, a.issues.TotalCost(), a.width)

	var viewContent string
	var viewHelp string
	switch a.activeTab {
	case tabDashboard:
		viewContent = a.dashboard.View()
		viewHelp = a.dashboard.ShortHelp()
	case tabIssues:
		viewContent = a.issues.View()
		viewHelp = a.issues.ShortHelp()
	case tabActions:
		viewContent = a.actions.View()
		viewHelp = a.actions.ShortHelp()
	}

	footer := components.RenderFooter(viewHelp, a.width)

	return fmt.Sprintf("%s\n\n%s\n\n%s", header, viewContent, footer)
}

// --- Messages ---

type eventMsg orchestrator.Event
type tickMsg time.Time

type gsdStateMsg struct {
	state *gsdstate.State
	err   error
}

type boardPollMsg struct {
	issues []tracker.Issue
	err    error
}

// --- Commands ---

func (a *App) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-a.orch.Events()
		if !ok {
			return nil
		}
		return eventMsg(event)
	}
}

func (a *App) tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (a *App) fetchGSDState() tea.Cmd {
	gsd := a.gsd
	return func() tea.Msg {
		if gsd == nil {
			return gsdStateMsg{nil, fmt.Errorf("no GSD provider configured")}
		}
		state, err := gsd.Get(context.Background())
		return gsdStateMsg{state, err}
	}
}

func (a *App) pollBoard() tea.Cmd {
	trk := a.orch.Tracker()
	return func() tea.Msg {
		issues, err := trk.Poll(context.Background())
		return boardPollMsg{issues, err}
	}
}
