package protocol

import (
	"fmt"
	"slices"
)

// ReasoningCapabilities describes the bound model, not merely its API family.
// Zero output limit means unknown; context-window limits must never fill it.
type ReasoningCapabilities struct {
	Kind               string   `json:"kind"`
	DisabledLevels     []string `json:"disabledLevels,omitempty"`
	BetweenToolsLevels []string `json:"betweenToolsLevels,omitempty"`
	Levels             []string `json:"levels,omitempty"`
	Disable            bool     `json:"disable"`
	Manual             bool     `json:"manual,omitempty"`
	Interleaved        bool     `json:"interleaved,omitempty"`
	MinBudget          int64    `json:"minBudget,omitempty"`
	MaxBudget          int64    `json:"maxBudget,omitempty"`
	MaxOutputTokens    int64    `json:"maxOutputTokens,omitempty"`
	DefaultEffort      string   `json:"defaultEffort,omitempty"`
	DefaultMode        string   `json:"defaultMode,omitempty"`
	Source             string   `json:"source,omitempty"`
}

func (cap ReasoningCapabilities) Validate() error {
	if !slices.Contains([]string{"unknown", "none", "budget", "level", "adaptive"}, cap.Kind) || cap.MinBudget < 0 || cap.MaxBudget < 0 || cap.MaxOutputTokens < 0 || (cap.MaxBudget > 0 && cap.MaxBudget < cap.MinBudget) {
		return fmt.Errorf("invalid reasoning capability kind or bounds")
	}
	if (cap.Kind == "level" || cap.Kind == "adaptive") && len(cap.Levels) == 0 {
		return fmt.Errorf("level capability requires supported levels")
	}
	if cap.DefaultEffort != "" && !slices.Contains(cap.Levels, cap.DefaultEffort) {
		return fmt.Errorf("default effort must be a supported level")
	}
	if !slices.Contains([]string{"", "enabled", "disabled", "auto"}, cap.DefaultMode) {
		return fmt.Errorf("invalid default thinking mode")
	}
	seen := map[string]bool{}
	for _, level := range cap.Levels {
		if !slices.Contains([]string{"minimal", "low", "medium", "high", "xhigh", "max"}, level) || seen[level] {
			return fmt.Errorf("invalid or duplicate reasoning level %q", level)
		}
		seen[level] = true
	}
	for _, level := range append(slices.Clone(cap.DisabledLevels), cap.BetweenToolsLevels...) {
		if !slices.Contains(cap.Levels, level) {
			return fmt.Errorf("mode-specific levels must be supported effort levels")
		}
	}
	return nil
}
