package seed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanObjective(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "03")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
phase: 03-frontend
plan: 01
---

<objective>
Build and push the two new Docker images. The existing image only contains server.py which does NOT serve the LangGraph API.
</objective>

<tasks>
Some tasks here.
</tasks>
`
	if err := os.WriteFile(filepath.Join(planDir, "PLAN-01.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// With already-padded plan number
	full, summary, err := PlanObjective(dir, "03", "01")
	if err != nil {
		t.Fatal(err)
	}
	if full == "" {
		t.Error("expected non-empty full objective")
	}
	if want := "Build and push the two new Docker images."; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}

	// With unpadded plan number (as stclaude returns)
	full2, summary2, err := PlanObjective(dir, "03", "1")
	if err != nil {
		t.Fatalf("unpadded planNumber should work: %v", err)
	}
	if full2 != full || summary2 != summary {
		t.Errorf("unpadded planNumber gave different results: full=%q summary=%q", full2, summary2)
	}
}

func TestPlanObjective_PhasePrefixedNaming(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "03-gitops-engine")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `<objective>
Deploy ArgoCD with GitOps workflow.
</objective>
`
	if err := os.WriteFile(filepath.Join(planDir, "03-01-PLAN.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, summary, err := PlanObjective(dir, "03", "01")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Deploy ArgoCD with GitOps workflow."; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
}

func TestPlanObjective_PlanPrefixedNaming(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "07-core-check-pipeline")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `<objective>
Implement health check pipeline.
</objective>
`
	if err := os.WriteFile(filepath.Join(planDir, "01-PLAN.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, summary, err := PlanObjective(dir, "07", "01")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Implement health check pipeline."; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
}

func TestPlanObjective_SinglePlan(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "01")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `<objective>
Bootstrap the infrastructure.
</objective>
`
	if err := os.WriteFile(filepath.Join(planDir, "PLAN.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, summary, err := PlanObjective(dir, "01", "01")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Bootstrap the infrastructure."; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
}

func TestPlanObjective_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, err := PlanObjective(dir, "99", "01")
	if err == nil {
		t.Error("expected error for missing phase dir")
	}
}

func TestPlanObjective_NoObjectiveTag(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "01")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "PLAN-01.md"), []byte("no objective here"), 0o644); err != nil {
		t.Fatal(err)
	}

	full, summary, err := PlanObjective(dir, "01", "01")
	if err != nil {
		t.Fatal(err)
	}
	if full != "" || summary != "" {
		t.Errorf("expected empty results for missing objective tag, got full=%q summary=%q", full, summary)
	}
}

func TestExtractPlanNumber(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"PLAN.md", "01"},
		{"PLAN-01.md", "01"},
		{"PLAN-03.md", "03"},
		{"01-PLAN.md", "01"},
		{"03-01-PLAN.md", "01"},
		{"03-02-PLAN.md", "02"},
		{"03.1-01-PLAN.md", "01"},
		{"setup-PLAN.md", "01"},
		{"RESEARCH.md", ""},
		{"03-SUMMARY.md", ""},
		{"03-CONTEXT.md", ""},
		{"readme.md", ""},
	}
	for _, tt := range tests {
		got := extractPlanNumber(tt.name)
		if got != tt.want {
			t.Errorf("extractPlanNumber(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDiscoverPlans(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "02")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"PLAN-01.md", "PLAN-02.md", "PLAN-03.md", "RESEARCH.md"} {
		if err := os.WriteFile(filepath.Join(planDir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plans := DiscoverPlans(dir, "02", "phase-2-id")
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}
	for i, want := range []string{"01", "02", "03"} {
		if plans[i].PlanNumber != want {
			t.Errorf("plans[%d].PlanNumber = %q, want %q", i, plans[i].PlanNumber, want)
		}
		if plans[i].PhaseID != "phase-2-id" {
			t.Errorf("plans[%d].PhaseID = %q, want %q", i, plans[i].PhaseID, "phase-2-id")
		}
	}
}

func TestDiscoverPlans_PhasePrefixed(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "04-platform-services")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"04-01-PLAN.md", "04-02-PLAN.md", "04-RESEARCH.md", "04-CONTEXT.md"} {
		if err := os.WriteFile(filepath.Join(planDir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plans := DiscoverPlans(dir, "04", "p4")
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].PlanNumber != "01" || plans[1].PlanNumber != "02" {
		t.Errorf("unexpected plan numbers: %v, %v", plans[0].PlanNumber, plans[1].PlanNumber)
	}
}

func TestDiscoverPlans_PlanPrefixed(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "07-core-check-pipeline")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"01-PLAN.md", "02-PLAN.md", "03-PLAN.md"} {
		if err := os.WriteFile(filepath.Join(planDir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plans := DiscoverPlans(dir, "07", "p7")
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}
	for i, want := range []string{"01", "02", "03"} {
		if plans[i].PlanNumber != want {
			t.Errorf("plans[%d].PlanNumber = %q, want %q", i, plans[i].PlanNumber, want)
		}
	}
}

func TestDiscoverPlans_SinglePlan(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "01-foundation")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(planDir, "PLAN.md"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	plans := DiscoverPlans(dir, "01", "p1")
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].PlanNumber != "01" {
		t.Errorf("PlanNumber = %q, want %q", plans[0].PlanNumber, "01")
	}
}

func TestDiscoverPlans_NoDir(t *testing.T) {
	plans := DiscoverPlans(t.TempDir(), "99", "x")
	if len(plans) != 0 {
		t.Errorf("expected 0 plans for missing dir, got %d", len(plans))
	}
}

func TestFindPhaseDir(t *testing.T) {
	dir := t.TempDir()
	phasesRoot := filepath.Join(dir, ".planning", "phases")

	os.MkdirAll(filepath.Join(phasesRoot, "03"), 0o755)
	os.MkdirAll(filepath.Join(phasesRoot, "04-platform-services"), 0o755)

	if got := findPhaseDir(dir, "03"); got == "" {
		t.Error("expected to find bare number dir")
	}
	if got := findPhaseDir(dir, "04"); got == "" {
		t.Error("expected to find slug dir")
	}
	if got := findPhaseDir(dir, "99"); got != "" {
		t.Errorf("expected empty for missing phase, got %q", got)
	}
}

func TestReadPlanMeta_DependsOn(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "03")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
phase: 03-frontend
plan: 02
depends_on: [01]
---

<objective>
Deploy the frontend after images are built.
</objective>
`
	if err := os.WriteFile(filepath.Join(planDir, "PLAN-02.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadPlanMeta(dir, "03", "02")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(meta.DependsOn) != 1 || meta.DependsOn[0] != "01" {
		t.Errorf("DependsOn = %v, want [01]", meta.DependsOn)
	}
}

func TestReadPlanMeta_MultipleDeps(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "04")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
phase: 04-services
plan: 03
depends_on: [01, 02]
---

<objective>
Configure ingress after services are ready.
</objective>
`
	if err := os.WriteFile(filepath.Join(planDir, "PLAN-03.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadPlanMeta(dir, "04", "03")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.DependsOn) != 2 {
		t.Fatalf("DependsOn length = %d, want 2", len(meta.DependsOn))
	}
	if meta.DependsOn[0] != "01" || meta.DependsOn[1] != "02" {
		t.Errorf("DependsOn = %v, want [01 02]", meta.DependsOn)
	}
}

func TestReadPlanMeta_EmptyDeps(t *testing.T) {
	dir := t.TempDir()
	planDir := filepath.Join(dir, ".planning", "phases", "01")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
phase: 01-foundation
plan: 01
depends_on: []
---

<objective>
First plan, no deps.
</objective>
`
	if err := os.WriteFile(filepath.Join(planDir, "PLAN-01.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadPlanMeta(dir, "01", "01")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, want empty", meta.DependsOn)
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Short text", "Short text"},
		{"First sentence. Second sentence.", "First sentence."},
		{"First line\nSecond line", "First line"},
		{
			"This is a very long sentence that exceeds eighty characters and should be truncated to fit within limits properly",
			"This is a very long sentence that exceeds eighty characters and should be tru...",
		},
	}
	for _, tt := range tests {
		got := firstSentence(tt.input)
		if got != tt.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
