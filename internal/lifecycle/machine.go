package lifecycle

import (
	"fmt"
	"sync"
)

const MaxValidationRetries = 2

type StateMachine struct {
	mu                sync.Mutex
	currentPhase      Phase
	validationRetries int
	issueNumber       int
}

func NewStateMachine(issueNumber int, initialPhase Phase) *StateMachine {
	return &StateMachine{
		currentPhase: initialPhase,
		issueNumber:  issueNumber,
	}
}

func (sm *StateMachine) CurrentPhase() Phase {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.currentPhase
}

func (sm *StateMachine) ValidationRetries() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.validationRetries
}

// Advance moves to the next phase. For validation failures, it retries
// by going back to InProgress (up to MaxValidationRetries times).
func (sm *StateMachine) Advance() (Phase, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	next, err := NextPhase(sm.currentPhase)
	if err != nil {
		return sm.currentPhase, err
	}

	sm.currentPhase = next
	return next, nil
}

// RetryValidation moves back to InProgress if retries remain,
// otherwise advances to HumanReview.
func (sm *StateMachine) RetryValidation() (Phase, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.currentPhase != PhaseValidation {
		return sm.currentPhase, fmt.Errorf("can only retry from Validation phase, currently in %s", sm.currentPhase)
	}

	if sm.validationRetries >= MaxValidationRetries {
		sm.currentPhase = PhaseHumanReview
		return PhaseHumanReview, nil
	}

	sm.validationRetries++
	sm.currentPhase = PhaseInProgress
	return PhaseInProgress, nil
}

// Cancel moves to Cancelled state from any non-terminal state.
func (sm *StateMachine) Cancel() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if IsTerminal(sm.currentPhase) {
		return fmt.Errorf("cannot cancel from terminal state %s", sm.currentPhase)
	}

	sm.currentPhase = PhaseCancelled
	return nil
}
