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
