package agent

import (
	"fmt"

	"github.com/flosch/pongo2/v6"
)

// PhasePrompts contains the default prompt templates for each GSD phase
var PhasePrompts = map[string]string{
	"Research": `You are researching issue #{{ issue.number }}: {{ issue.title }}

## Issue Description
{{ issue.body }}

## Instructions
Explore the codebase thoroughly to understand:
1. What code is relevant to this issue
2. What the current behavior is
3. What changes would be needed
4. Any risks or dependencies

Write your findings to .maestro/research.md in the workspace.
Be thorough but concise. Focus on actionable insights.`,

	"Planning": `You are planning the implementation for issue #{{ issue.number }}: {{ issue.title }}

## Issue Description
{{ issue.body }}

## Research Findings
Review .maestro/research.md for context from the research phase.

## Instructions
Create a detailed implementation plan:
1. Break the work into concrete tasks
2. Define acceptance criteria for each task
3. Identify verification steps
4. Note any risks or edge cases

Write the plan to .maestro/plan.md in the workspace.`,

	"In Progress": `You are implementing issue #{{ issue.number }}: {{ issue.title }}

## Issue Description
{{ issue.body }}

## Plan
Review .maestro/plan.md for the implementation plan.

## Instructions
Execute the plan:
1. Follow the tasks in order
2. Write tests alongside implementation
3. Make atomic commits with clear messages
4. Log progress to .maestro/progress.md

Focus on correctness and simplicity. Follow existing code patterns.`,

	"Validation": `You are validating the implementation for issue #{{ issue.number }}: {{ issue.title }}

## Issue Description
{{ issue.body }}

## Plan
Review .maestro/plan.md for acceptance criteria.

## Instructions
Validate the implementation:
1. Run all tests (go test -race ./...)
2. Check each acceptance criterion from the plan
3. Verify no regressions
4. Review code quality

Write results to .maestro/validation.md.
Exit with a clear PASS or FAIL verdict at the end.
If FAIL, explain what needs to be fixed.`,
}

// RenderPrompt renders a phase prompt with issue context
func RenderPrompt(phaseTemplate string, issueCtx map[string]interface{}) (string, error) {
	tpl, err := pongo2.FromString(phaseTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	result, err := tpl.Execute(pongo2.Context(issueCtx))
	if err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	return result, nil
}

// RenderCustomPrompt renders the user's custom prompt template from WORKFLOW.md
func RenderCustomPrompt(template string, issueCtx map[string]interface{}, phase string) (string, error) {
	// Add phase to context
	ctx := make(map[string]interface{})
	for k, v := range issueCtx {
		ctx[k] = v
	}
	ctx["phase"] = phase

	tpl, err := pongo2.FromString(template)
	if err != nil {
		return "", fmt.Errorf("failed to parse custom template: %w", err)
	}

	result, err := tpl.Execute(pongo2.Context(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to render custom template: %w", err)
	}

	return result, nil
}

// IssueContext creates a pongo2-compatible context map from issue data
func IssueContext(number int, title, body string, labels []string) map[string]interface{} {
	return map[string]interface{}{
		"issue": map[string]interface{}{
			"number": number,
			"title":  title,
			"body":   body,
			"labels": labels,
		},
	}
}
