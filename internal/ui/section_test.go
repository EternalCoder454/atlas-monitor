package ui

import (
	"testing"

	"atlas-monitor/internal/config"
)

// TestSectionFoldSurvivesTheRename: section headings went from upper case to
// sentence case, and the fold state is stored by heading. A section folded as
// "CORES" has to come back folded as "Cores", not open again.
func TestSectionFoldSurvivesTheRename(t *testing.T) {
	s := &config.Settings{CollapsedSections: []string{"CORES"}}
	if !sectionCollapsed(s, "Cores") {
		t.Error("a section folded under its old upper-case heading came back open")
	}
	if sectionCollapsed(s, "Details") {
		t.Error("an unrelated section reads as folded")
	}
	if sectionCollapsed(nil, "Cores") {
		t.Error("no settings should mean nothing is folded")
	}
}
