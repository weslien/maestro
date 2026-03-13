package seed

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/weslien/maestro/internal/gsdstate"
)

var (
	objectiveRe  = regexp.MustCompile(`(?s)<objective>\s*(.*?)\s*</objective>`)
	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---`)
	dependsOnRe   = regexp.MustCompile(`(?m)^depends_on:\s*\[([^\]]*)\]`)
)

// PlanMeta holds metadata extracted from a plan file's frontmatter.
type PlanMeta struct {
	Objective string
	Summary   string
	DependsOn []string // plan numbers this plan depends on
}

// ReadPlanMeta reads a plan file and extracts objective and depends_on.
func ReadPlanMeta(repoDir, phaseNumber, planNumber string) (*PlanMeta, error) {
	dir := findPhaseDir(repoDir, phaseNumber)
	if dir == "" {
		return nil, fmt.Errorf("phase directory not found for phase %s", phaseNumber)
	}

	path := findPlanFile(dir, phaseNumber, planNumber)
	if path == "" {
		return nil, fmt.Errorf("no plan file found for phase %s plan %s", phaseNumber, planNumber)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read plan file: %w", err)
	}

	meta := &PlanMeta{}

	// Extract objective
	if m := objectiveRe.FindSubmatch(data); m != nil {
		meta.Objective = strings.TrimSpace(string(m[1]))
		meta.Summary = firstSentence(meta.Objective)
	}

	// Extract depends_on from frontmatter
	if fm := frontmatterRe.FindSubmatch(data); fm != nil {
		if dm := dependsOnRe.FindSubmatch(fm[1]); dm != nil {
			items := strings.TrimSpace(string(dm[1]))
			if items != "" {
				for _, item := range strings.Split(items, ",") {
					item = strings.TrimSpace(item)
					if item != "" {
						meta.DependsOn = append(meta.DependsOn, item)
					}
				}
			}
		}
	}

	return meta, nil
}

// planNumberRe extracts the plan number from a plan file name.
// GSD's canonical detection is: endsWith("-PLAN.md") || name == "PLAN.md"
// The plan number is extracted from the prefix before -PLAN.md.
// Examples:
//
//	PLAN.md           → plan "01" (single plan, implicit)
//	01-PLAN.md        → plan "01"
//	03-01-PLAN.md     → plan "01" (last numeric segment before -PLAN)
//	PLAN-01.md        → plan "01" (legacy df-repo style, also ends with -PLAN after removing .md? No.)
//
// For PLAN-NN.md (legacy), we use a separate pattern.
var (
	gsdPlanRe    = regexp.MustCompile(`^(.+)-PLAN\.md$`)    // anything-PLAN.md
	legacyPlanRe = regexp.MustCompile(`^PLAN-(\d+)\.md$`)   // PLAN-01.md
	lastDigitsRe = regexp.MustCompile(`(\d+)$`)              // trailing digits
)

// PlanObjective reads a plan file and extracts the <objective> content.
// Returns the full objective text and a short summary (first sentence/line).
func PlanObjective(repoDir, phaseNumber, planNumber string) (full string, summary string, err error) {
	meta, err := ReadPlanMeta(repoDir, phaseNumber, planNumber)
	if err != nil {
		return "", "", err
	}
	return meta.Objective, meta.Summary, nil
}

// findPlanFile locates a plan file for the given plan number within a phase directory.
// Tries GSD canonical patterns first, then legacy PLAN-NN.md.
func findPlanFile(dir, phaseNumber, planNumber string) string {
	padded := zeroPad(planNumber, 2)
	candidates := []string{
		fmt.Sprintf("%s-%s-PLAN.md", phaseNumber, padded), // 03-01-PLAN.md
		fmt.Sprintf("%s-PLAN.md", padded),                  // 01-PLAN.md
		fmt.Sprintf("PLAN-%s.md", padded),                  // PLAN-01.md (legacy)
	}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// Single-plan phase: PLAN.md
	if planNumber == "1" || planNumber == "01" {
		path := filepath.Join(dir, "PLAN.md")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// zeroPad left-pads a numeric string to the given width with zeros.
func zeroPad(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// DiscoverPlans scans the phase directory for plan files and returns synthetic
// Plan entries. Uses GSD's canonical detection: endsWith("-PLAN.md") || name == "PLAN.md".
func DiscoverPlans(repoDir, phaseNumber, phaseID string) []gsdstate.Plan {
	dir := findPhaseDir(repoDir, phaseNumber)
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var plans []gsdstate.Plan
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		num := extractPlanNumber(name)
		if num == "" {
			continue
		}
		if seen[num] {
			continue
		}
		seen[num] = true
		plans = append(plans, gsdstate.Plan{
			PhaseID:    phaseID,
			PlanNumber: num,
			Status:     "pending",
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].PlanNumber < plans[j].PlanNumber
	})
	return plans
}

// extractPlanNumber returns the plan number from a plan filename, or "" if not a plan file.
// Matches GSD canonical detection: endsWith("-PLAN.md") || name == "PLAN.md",
// plus legacy PLAN-NN.md.
func extractPlanNumber(name string) string {
	// GSD canonical: PLAN.md (single plan)
	if name == "PLAN.md" {
		return "01"
	}
	// GSD canonical: *-PLAN.md — extract trailing digits from prefix
	if m := gsdPlanRe.FindStringSubmatch(name); m != nil {
		prefix := m[1]
		if dm := lastDigitsRe.FindStringSubmatch(prefix); dm != nil {
			return dm[1]
		}
		// Prefix has no digits (e.g., "setup-PLAN.md") — treat as plan 01
		return "01"
	}
	// Legacy: PLAN-NN.md
	if m := legacyPlanRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// findPhaseDir locates the phase directory under .planning/phases/.
// Handles bare number dirs ("03") and slug dirs ("03-gitops-engine").
func findPhaseDir(repoDir, phaseNumber string) string {
	phasesRoot := filepath.Join(repoDir, ".planning", "phases")

	// Try exact match first (bare number)
	exact := filepath.Join(phasesRoot, phaseNumber)
	if info, err := os.Stat(exact); err == nil && info.IsDir() {
		return exact
	}

	// Scan for slug dirs starting with the phase number prefix
	entries, err := os.ReadDir(phasesRoot)
	if err != nil {
		return ""
	}
	prefix := phaseNumber + "-"
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return filepath.Join(phasesRoot, e.Name())
		}
	}
	return ""
}

// firstSentence returns the first sentence or line of text, truncated to 80 chars.
func firstSentence(s string) string {
	// Take first line
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		s = s[:idx]
	}
	// Take first sentence
	if idx := strings.Index(s, ". "); idx > 0 {
		s = s[:idx+1]
	}
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
