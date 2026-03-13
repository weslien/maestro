package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// GHEnricher fetches project field values via the gh CLI.
type GHEnricher struct{}

// NewGHEnricher creates an enricher that uses gh api graphql.
func NewGHEnricher() *GHEnricher {
	return &GHEnricher{}
}

func (e *GHEnricher) Enrich(ctx context.Context, contentNodeID string) ([]FieldValue, error) {
	query := fmt.Sprintf(`query {
  node(id: %q) {
    ... on Issue {
      projectItems(first: 5) {
        nodes {
          fieldValues(first: 20) {
            nodes {
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                field { ... on ProjectV2SingleSelectField { name } }
              }
              ... on ProjectV2ItemFieldTextValue {
                text
                field { ... on ProjectV2Field { name } }
              }
              ... on ProjectV2ItemFieldNumberValue {
                number
                field { ... on ProjectV2Field { name } }
              }
              ... on ProjectV2ItemFieldDateValue {
                date
                field { ... on ProjectV2Field { name } }
              }
            }
          }
        }
      }
    }
  }
}`, contentNodeID)

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql", "-f", "query="+query)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api graphql failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("failed to run gh: %w", err)
	}

	return parseEnrichmentResponse(out)
}

func parseEnrichmentResponse(data []byte) ([]FieldValue, error) {
	var resp struct {
		Data struct {
			Node struct {
				ProjectItems struct {
					Nodes []struct {
						FieldValues struct {
							Nodes []json.RawMessage `json:"nodes"`
						} `json:"fieldValues"`
					} `json:"nodes"`
				} `json:"projectItems"`
			} `json:"node"`
		} `json:"data"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse enrichment response: %w", err)
	}

	var fields []FieldValue
	for _, item := range resp.Data.Node.ProjectItems.Nodes {
		for _, raw := range item.FieldValues.Nodes {
			fv, ok := parseFieldValueNode(raw)
			if ok {
				fields = append(fields, fv)
			}
		}
	}

	return fields, nil
}

func parseFieldValueNode(raw json.RawMessage) (FieldValue, bool) {
	// Try single select
	var ss struct {
		Name  string `json:"name"`
		Field struct {
			Name string `json:"name"`
		} `json:"field"`
	}
	if json.Unmarshal(raw, &ss) == nil && ss.Field.Name != "" && ss.Name != "" {
		return FieldValue{FieldName: ss.Field.Name, Value: ss.Name}, true
	}

	// Try text
	var tv struct {
		Text  string `json:"text"`
		Field struct {
			Name string `json:"name"`
		} `json:"field"`
	}
	if json.Unmarshal(raw, &tv) == nil && tv.Field.Name != "" && tv.Text != "" {
		return FieldValue{FieldName: tv.Field.Name, Value: tv.Text}, true
	}

	// Try number
	var nv struct {
		Number float64 `json:"number"`
		Field  struct {
			Name string `json:"name"`
		} `json:"field"`
	}
	if json.Unmarshal(raw, &nv) == nil && nv.Field.Name != "" && nv.Number != 0 {
		return FieldValue{FieldName: nv.Field.Name, Value: fmt.Sprintf("%g", nv.Number)}, true
	}

	// Try date
	var dv struct {
		Date  string `json:"date"`
		Field struct {
			Name string `json:"name"`
		} `json:"field"`
	}
	if json.Unmarshal(raw, &dv) == nil && dv.Field.Name != "" && dv.Date != "" {
		return FieldValue{FieldName: dv.Field.Name, Value: dv.Date}, true
	}

	return FieldValue{}, false
}
