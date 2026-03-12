package orchestrator

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/weslien/maestro/internal/agent"
	"github.com/weslien/maestro/internal/config"
	"github.com/weslien/maestro/internal/lifecycle"
	"github.com/weslien/maestro/internal/tracker"
	"github.com/weslien/maestro/internal/workspace"
)

// Event represents something that happened in the orchestrator
type Event struct {
	Time        time.Time
	IssueNumber int
	IssueTitle  string
	Phase       string
	Type        string // "phase_start", "phase_end", "error", "cost_update", "status_change"
	Message     string
	CostUSD     float64
}

// IssueState tracks the runtime state of an issue being processed
type IssueState struct {
	Issue      tracker.Issue
	Machine    *lifecycle.StateMachine
	Runner     *agent.ClaudeRunner
	SessionID  string
	StartTime  time.Time
	CostUSD    float64
	TmuxName   string
	mu         sync.Mutex
}

func (is *IssueState) UpdateCost(cost float64) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.CostUSD = cost
}

func (is *IssueState) GetCost() float64 {
	is.mu.Lock()
	defer is.mu.Unlock()
	return is.CostUSD
}

// Orchestrator manages the poll loop and dispatches work
type Orchestrator struct {
	cfg       *config.Config
	tracker   tracker.Tracker
	workspace *workspace.Manager
	tmux      *agent.TmuxManager
	events    chan Event
	semaphore chan struct{}

	mu       sync.Mutex
	active   map[int]*IssueState // issue number -> state
	cancel   context.CancelFunc
	stopped  bool
}

// New creates a new Orchestrator
func New(cfg *config.Config, trk tracker.Tracker) *Orchestrator {
	wm := workspace.NewManager(cfg.Workspace.Root, cfg.Workspace.Hooks.AfterCreate)
	tm := agent.NewTmuxManager(cfg.Tmux.SessionPrefix)

	return &Orchestrator{
		cfg:       cfg,
		tracker:   trk,
		workspace: wm,
		tmux:      tm,
		events:    make(chan Event, 1000),
		semaphore: make(chan struct{}, cfg.Agent.MaxConcurrent),
		active:    make(map[int]*IssueState),
	}
}

// Events returns the event channel for the TUI
func (o *Orchestrator) Events() <-chan Event {
	return o.events
}

// ActiveIssues returns a snapshot of active issue states
func (o *Orchestrator) ActiveIssues() []*IssueState {
	o.mu.Lock()
	defer o.mu.Unlock()
	states := make([]*IssueState, 0, len(o.active))
	for _, s := range o.active {
		states = append(states, s)
	}
	return states
}

// Run starts the poll loop. Blocks until context is cancelled.
func (o *Orchestrator) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	defer cancel()

	// Cleanup orphaned tmux sessions on startup
	if err := o.tmux.CleanupOrphaned(ctx); err != nil {
		log.Printf("Warning: failed to cleanup orphaned tmux sessions: %v", err)
	}

	o.emit(Event{
		Time:    time.Now(),
		Type:    "system",
		Message: fmt.Sprintf("Maestro started, polling every %s", o.cfg.Polling.Interval),
	})

	ticker := time.NewTicker(o.cfg.Polling.Interval)
	defer ticker.Stop()

	// Initial poll
	o.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			o.shutdown()
			return ctx.Err()
		case <-ticker.C:
			o.poll(ctx)
		}
	}
}

// Stop gracefully stops the orchestrator
func (o *Orchestrator) Stop() {
	o.mu.Lock()
	o.stopped = true
	o.mu.Unlock()

	if o.cancel != nil {
		o.cancel()
	}
}

// Tracker returns the underlying tracker for direct queries
func (o *Orchestrator) Tracker() tracker.Tracker {
	return o.tracker
}

// Config returns the orchestrator config
func (o *Orchestrator) Config() *config.Config {
	return o.cfg
}

// StartIssueByNumber fetches an issue from the tracker and starts processing it.
// Returns an error if the issue is already active or not in an actionable phase.
func (o *Orchestrator) StartIssueByNumber(ctx context.Context, issueNumber int) error {
	o.mu.Lock()
	_, running := o.active[issueNumber]
	o.mu.Unlock()
	if running {
		return fmt.Errorf("issue #%d is already being processed", issueNumber)
	}

	// Poll to find the issue with its current phase
	issues, err := o.tracker.Poll(ctx)
	if err != nil {
		return fmt.Errorf("failed to poll tracker: %w", err)
	}

	var target *tracker.Issue
	for i := range issues {
		if issues[i].Number == issueNumber {
			target = &issues[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("issue #%d not found on project board", issueNumber)
	}

	phase, err := lifecycle.ParsePhase(target.Status)
	if err != nil {
		return fmt.Errorf("issue #%d has unknown phase %q", issueNumber, target.Status)
	}
	if !lifecycle.IsActionable(phase) {
		return fmt.Errorf("issue #%d is in %s — not actionable", issueNumber, target.Status)
	}

	o.startIssue(ctx, *target, phase)
	return nil
}

// AdvanceIssuePhase moves an issue to a specific phase on the tracker without processing it
func (o *Orchestrator) AdvanceIssuePhase(ctx context.Context, issueNumber int, newPhase string) error {
	issues, err := o.tracker.Poll(ctx)
	if err != nil {
		return fmt.Errorf("failed to poll tracker: %w", err)
	}

	for _, issue := range issues {
		if issue.Number == issueNumber {
			return o.tracker.UpdateStatus(ctx, issue, newPhase)
		}
	}
	return fmt.Errorf("issue #%d not found on project board", issueNumber)
}

// StopIssue stops processing a specific issue
func (o *Orchestrator) StopIssue(issueNumber int) error {
	o.mu.Lock()
	state, ok := o.active[issueNumber]
	o.mu.Unlock()

	if !ok {
		return fmt.Errorf("issue #%d is not being processed", issueNumber)
	}

	if state.Runner != nil {
		state.Runner.Stop()
	}

	return nil
}

func (o *Orchestrator) poll(ctx context.Context) {
	issues, err := o.tracker.Poll(ctx)
	if err != nil {
		o.emit(Event{
			Time:    time.Now(),
			Type:    "error",
			Message: fmt.Sprintf("Poll failed: %v", err),
		})
		return
	}

	for _, issue := range issues {
		phase, err := lifecycle.ParsePhase(issue.Status)
		if err != nil {
			continue
		}

		if !lifecycle.IsActionable(phase) {
			continue
		}

		// Check if already processing
		o.mu.Lock()
		_, running := o.active[issue.Number]
		o.mu.Unlock()

		if running {
			continue
		}

		// Check for cancellation
		if issue.Status == "Cancelled" {
			o.mu.Lock()
			if state, ok := o.active[issue.Number]; ok {
				state.Runner.Stop()
				delete(o.active, issue.Number)
			}
			o.mu.Unlock()
			continue
		}

		// Start processing
		o.startIssue(ctx, issue, phase)
	}
}

func (o *Orchestrator) startIssue(ctx context.Context, issue tracker.Issue, startPhase lifecycle.Phase) {
	// Acquire semaphore
	select {
	case o.semaphore <- struct{}{}:
	case <-ctx.Done():
		return
	}

	machine := lifecycle.NewStateMachine(issue.Number, startPhase)

	state := &IssueState{
		Issue:     issue,
		Machine:   machine,
		StartTime: time.Now(),
		TmuxName:  o.tmux.SessionName(issue.Number),
	}

	o.mu.Lock()
	o.active[issue.Number] = state
	o.mu.Unlock()

	go func() {
		defer func() {
			<-o.semaphore // Release semaphore
			o.mu.Lock()
			delete(o.active, issue.Number)
			o.mu.Unlock()
			// Destroy tmux session
			o.tmux.DestroySession(context.Background(), issue.Number)
		}()

		o.processIssue(ctx, state)
	}()
}

func (o *Orchestrator) processIssue(ctx context.Context, state *IssueState) {
	issue := state.Issue

	// Ensure workspace
	workDir, err := o.workspace.Ensure(ctx, issue.Number)
	if err != nil {
		o.emit(Event{
			Time:        time.Now(),
			IssueNumber: issue.Number,
			IssueTitle:  issue.Title,
			Type:        "error",
			Message:     fmt.Sprintf("Failed to create workspace: %v", err),
		})
		return
	}

	// Session ID persists across phases for context continuity
	sessionID := fmt.Sprintf("maestro-%d", issue.Number)

	for {
		if ctx.Err() != nil {
			return
		}

		currentPhase := state.Machine.CurrentPhase()
		if lifecycle.IsTerminal(currentPhase) {
			return
		}
		if !lifecycle.IsActionable(currentPhase) {
			return
		}

		phaseName := currentPhase.String()

		o.emit(Event{
			Time:        time.Now(),
			IssueNumber: issue.Number,
			IssueTitle:  issue.Title,
			Phase:       phaseName,
			Type:        "phase_start",
			Message:     fmt.Sprintf("Starting %s phase", phaseName),
		})

		// Get model for this phase
		model := o.modelForPhase(currentPhase)

		// Create Claude runner
		runner := agent.NewClaudeRunner(model, o.cfg.Agent.PermissionMode, workDir, sessionID)
		state.Runner = runner

		// Create tmux session for observability
		logFile := runner.LogFile()
		if err := o.tmux.CreateSession(ctx, issue.Number, logFile); err != nil {
			log.Printf("Warning: failed to create tmux session for issue #%d: %v", issue.Number, err)
		}

		// Build prompt
		prompt, err := o.buildPrompt(issue, phaseName)
		if err != nil {
			o.emit(Event{
				Time:        time.Now(),
				IssueNumber: issue.Number,
				IssueTitle:  issue.Title,
				Phase:       phaseName,
				Type:        "error",
				Message:     fmt.Sprintf("Failed to build prompt: %v", err),
			})
			return
		}

		// Run Claude
		agentEvents := make(chan agent.StreamEvent, 100)
		go o.forwardEvents(issue, phaseName, agentEvents)

		result, err := runner.Run(ctx, prompt, agentEvents)
		close(agentEvents)

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			o.emit(Event{
				Time:        time.Now(),
				IssueNumber: issue.Number,
				IssueTitle:  issue.Title,
				Phase:       phaseName,
				Type:        "error",
				Message:     fmt.Sprintf("Claude failed: %v", err),
			})
			return
		}

		// Update cost
		if result != nil {
			state.UpdateCost(state.GetCost() + result.CostUSD)
			sessionID = result.SessionID
		}

		// Check budget
		if o.cfg.Agent.MaxBudgetPerIssue > 0 && state.GetCost() >= o.cfg.Agent.MaxBudgetPerIssue {
			o.emit(Event{
				Time:        time.Now(),
				IssueNumber: issue.Number,
				IssueTitle:  issue.Title,
				Phase:       phaseName,
				Type:        "error",
				Message:     fmt.Sprintf("Budget exceeded ($%.2f >= $%.2f), moving to Human Review", state.GetCost(), o.cfg.Agent.MaxBudgetPerIssue),
			})
			_ = o.tracker.UpdateStatus(ctx, issue, "Human Review")
			return
		}

		o.emit(Event{
			Time:        time.Now(),
			IssueNumber: issue.Number,
			IssueTitle:  issue.Title,
			Phase:       phaseName,
			Type:        "phase_end",
			Message:     fmt.Sprintf("Completed %s phase ($%.4f)", phaseName, result.CostUSD),
			CostUSD:     result.CostUSD,
		})

		// Handle validation failures
		if currentPhase == lifecycle.PhaseValidation && result != nil && !result.Success {
			nextPhase, err := state.Machine.RetryValidation()
			if err != nil {
				o.emit(Event{
					Time:        time.Now(),
					IssueNumber: issue.Number,
					IssueTitle:  issue.Title,
					Phase:       phaseName,
					Type:        "error",
					Message:     fmt.Sprintf("Retry validation error: %v", err),
				})
				return
			}
			if err := o.tracker.UpdateStatus(ctx, issue, nextPhase.String()); err != nil {
				o.emit(Event{
					Time:        time.Now(),
					IssueNumber: issue.Number,
					IssueTitle:  issue.Title,
					Type:        "error",
					Message:     fmt.Sprintf("Failed to update status: %v", err),
				})
			}
			continue
		}

		// Advance to next phase
		nextPhase, err := state.Machine.Advance()
		if err != nil {
			return
		}

		if err := o.tracker.UpdateStatus(ctx, issue, nextPhase.String()); err != nil {
			o.emit(Event{
				Time:        time.Now(),
				IssueNumber: issue.Number,
				IssueTitle:  issue.Title,
				Type:        "error",
				Message:     fmt.Sprintf("Failed to update status to %s: %v", nextPhase, err),
			})
			return
		}
	}
}

func (o *Orchestrator) buildPrompt(issue tracker.Issue, phase string) (string, error) {
	issueCtx := agent.IssueContext(issue.Number, issue.Title, issue.Body, issue.Labels)

	// Try custom template first
	if o.cfg.PromptTemplate != "" {
		return agent.RenderCustomPrompt(o.cfg.PromptTemplate, issueCtx, phase)
	}

	// Fall back to default phase prompts
	tmpl, ok := agent.PhasePrompts[phase]
	if !ok {
		return "", fmt.Errorf("no prompt template for phase %s", phase)
	}
	return agent.RenderPrompt(tmpl, issueCtx)
}

func (o *Orchestrator) modelForPhase(phase lifecycle.Phase) string {
	switch phase {
	case lifecycle.PhaseResearch:
		return o.cfg.Agent.ResearchModel
	case lifecycle.PhasePlanning:
		return o.cfg.Agent.PlanningModel
	case lifecycle.PhaseInProgress:
		return o.cfg.Agent.ExecutionModel
	case lifecycle.PhaseValidation:
		return o.cfg.Agent.ValidationModel
	default:
		return o.cfg.Agent.Model
	}
}

func (o *Orchestrator) forwardEvents(issue tracker.Issue, phase string, events <-chan agent.StreamEvent) {
	for ev := range events {
		msg := ""
		switch ev.Type {
		case agent.EventToolUse:
			msg = fmt.Sprintf("Using tool: %s", ev.ToolName)
		case agent.EventError:
			msg = fmt.Sprintf("Error: %s", ev.Error)
		case agent.EventResult:
			msg = fmt.Sprintf("Completed ($%.4f, %d turns)", ev.CostUSD, ev.NumTurns)
		default:
			continue
		}

		o.emit(Event{
			Time:        ev.Timestamp,
			IssueNumber: issue.Number,
			IssueTitle:  issue.Title,
			Phase:       phase,
			Type:        string(ev.Type),
			Message:     msg,
			CostUSD:     ev.CostUSD,
		})
	}
}

func (o *Orchestrator) shutdown() {
	o.mu.Lock()
	defer o.mu.Unlock()

	for num, state := range o.active {
		if state.Runner != nil {
			state.Runner.Stop()
		}
		o.tmux.DestroySession(context.Background(), num)
	}
}

func (o *Orchestrator) emit(event Event) {
	select {
	case o.events <- event:
	default:
		// Drop if full
	}
}
