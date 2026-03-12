package lifecycle

import "fmt"

type Phase int

const (
	PhaseBacklog Phase = iota
	PhaseTodo
	PhaseResearch
	PhasePlanning
	PhaseInProgress
	PhaseValidation
	PhaseHumanReview
	PhaseDone
	PhaseCancelled
)

var phaseNames = map[Phase]string{
	PhaseBacklog:     "Backlog",
	PhaseTodo:        "Todo",
	PhaseResearch:    "Research",
	PhasePlanning:    "Planning",
	PhaseInProgress:  "In Progress",
	PhaseValidation:  "Validation",
	PhaseHumanReview: "Human Review",
	PhaseDone:        "Done",
	PhaseCancelled:   "Cancelled",
}

var phaseByName map[string]Phase

func init() {
	phaseByName = make(map[string]Phase, len(phaseNames))
	for p, name := range phaseNames {
		phaseByName[name] = p
	}
}

func (p Phase) String() string {
	if name, ok := phaseNames[p]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(p))
}

func ParsePhase(name string) (Phase, error) {
	if p, ok := phaseByName[name]; ok {
		return p, nil
	}
	return 0, fmt.Errorf("unknown phase: %q", name)
}

// AllStatuses returns all status names for project setup
func AllStatuses() []string {
	return []string{
		"Backlog", "Todo", "Research", "Planning",
		"In Progress", "Validation", "Human Review", "Done", "Cancelled",
	}
}

// NextPhase returns the next phase in the GSD lifecycle
func NextPhase(current Phase) (Phase, error) {
	switch current {
	case PhaseTodo:
		return PhaseResearch, nil
	case PhaseResearch:
		return PhasePlanning, nil
	case PhasePlanning:
		return PhaseInProgress, nil
	case PhaseInProgress:
		return PhaseValidation, nil
	case PhaseValidation:
		return PhaseHumanReview, nil
	default:
		return 0, fmt.Errorf("no automatic transition from %s", current)
	}
}

// IsActionable returns true if the phase should trigger agent work
func IsActionable(p Phase) bool {
	switch p {
	case PhaseTodo, PhaseResearch, PhasePlanning, PhaseInProgress, PhaseValidation:
		return true
	default:
		return false
	}
}

// IsTerminal returns true if the phase is a final state
func IsTerminal(p Phase) bool {
	return p == PhaseDone || p == PhaseCancelled || p == PhaseHumanReview
}
