package tracker

import "time"

type Issue struct {
	ID            string
	Number        int
	Title         string
	Body          string
	Labels        []string
	Status        string
	ProjectItemID string
	URL           string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type StatusField struct {
	FieldID  string
	OptionID string
	Name     string
}

// ViewInfo describes a GitHub Projects V2 view and its configuration.
type ViewInfo struct {
	ID                    string
	Name                  string
	Number                int
	Layout                string // "TABLE_LAYOUT", "BOARD_LAYOUT", "ROADMAP_LAYOUT"
	Filter                string
	Fields                []ViewField
	GroupByFields         []ViewField
	VerticalGroupByFields []ViewField
}

// ViewField is a field reference within a view.
type ViewField struct {
	ID   string
	Name string
}
