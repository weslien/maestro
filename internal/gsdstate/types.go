package gsdstate

import "strings"

// State is the response from `stclaude get-state --json`
type State struct {
	OK   bool `json:"ok"`
	Data struct {
		Project Project `json:"project"`
		Phases  []Phase `json:"phases"`
		Plans   []Plan  `json:"plans"`
	} `json:"data"`
}

// Project holds GSD project metadata
type Project struct {
	Name      string `json:"name"`
	CoreValue string `json:"coreValue"`
}

// Phase represents a single GSD phase
type Phase struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Name   string `json:"name"`
	Status string `json:"status"` // "complete", "pending", "in_progress"
	Goal   string `json:"goal"`
}

// Plan represents a sub-item within a GSD phase
type Plan struct {
	ID         string `json:"id"`
	PhaseID    string `json:"phaseId"`
	PlanNumber string `json:"planNumber"`
	Status     string `json:"status"`
}

// PhaseToMaestro maps GSD phase status to maestro Phase field values.
// Handles both casing variants from stclaude (e.g., "Complete" and "complete").
func PhaseToMaestro(gsdStatus string) string {
	switch strings.ToLower(gsdStatus) {
	case "complete":
		return "Done"
	case "in_progress":
		return "In Progress"
	default:
		return "Backlog"
	}
}

// PlanToMaestro maps GSD plan status to maestro Phase field values.
func PlanToMaestro(gsdStatus string) string {
	return PhaseToMaestro(gsdStatus)
}

// NormalizeStatus lowercases a GSD status for consistent comparison
func NormalizeStatus(status string) string {
	return strings.ToLower(status)
}
