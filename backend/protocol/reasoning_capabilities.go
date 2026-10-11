package protocol

import (
	"fmt"
	"slices"
	"strings"
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

// Sources checked 2026-10-11. Series defaults are deliberately centralized;
// endpoint-specific exceptions precede their series, and aliases stay unknown.
func ResolveReasoningCapabilities(codec, model string) ReasoningCapabilities {
	model = strings.TrimPrefix(strings.ToLower(model), "models/")
	cap := ReasoningCapabilities{Kind: "unknown"}
	switch codec {
	case "gemini":
		cap.Source = "https://ai.google.dev/gemini-api/docs/generate-content/thinking"
		switch {
		case strings.Contains(model, "image"), strings.Contains(model, "tts"), strings.Contains(model, "audio"), strings.Contains(model, "live"):
			// These endpoints have independent controls; the text-model series
			// must not grant capabilities to an unverified media variant.
			return cap
		case strings.HasPrefix(model, "gemini-2.5-pro"):
			cap.Kind, cap.MinBudget, cap.MaxBudget, cap.DefaultMode = "budget", 128, 32768, "auto"
		case strings.HasPrefix(model, "gemini-2.5-flash-lite"):
			cap.Kind, cap.MinBudget, cap.MaxBudget, cap.Disable, cap.DefaultMode = "budget", 512, 24576, true, "disabled"
		case strings.HasPrefix(model, "gemini-2.5-flash"):
			cap.Kind, cap.MaxBudget, cap.Disable, cap.DefaultMode = "budget", 24576, true, "auto"
		case strings.HasPrefix(model, "gemini-3-"), strings.HasPrefix(model, "gemini-3."):
			cap.Kind, cap.DefaultMode, cap.DefaultEffort = "level", "enabled", "high"
			cap.Levels = []string{"minimal", "low", "medium", "high"}
			switch {
			case strings.HasPrefix(model, "gemini-3.1-pro"), strings.HasPrefix(model, "gemini-3.7-flash"), strings.HasPrefix(model, "gemini-3.8-flash"):
				cap.Levels = []string{"low", "medium", "high"}
				if strings.Contains(model, "flash") {
					cap.DefaultEffort = "medium"
				}
			case strings.HasPrefix(model, "gemini-3-pro"):
				cap.Levels = []string{"low", "high"}
			case strings.Contains(model, "flash-lite"):
				cap.DefaultEffort = "minimal"
			case strings.HasPrefix(model, "gemini-3.5-flash"), strings.HasPrefix(model, "gemini-3.6-flash"):
				cap.DefaultEffort = "medium"
			}
		}
	case "anthropic":
		cap.Source = "https://platform.claude.com/docs/en/build-with-claude/extended-thinking"
		if !strings.HasPrefix(model, "claude-3-7-sonnet") && !strings.HasPrefix(model, "claude-sonnet-4") && !strings.HasPrefix(model, "claude-opus-4") && !strings.HasPrefix(model, "claude-haiku-4-5") && !strings.HasPrefix(model, "claude-opus-5") && !strings.HasPrefix(model, "claude-sonnet-5") && !strings.HasPrefix(model, "claude-fable-5") && !strings.HasPrefix(model, "claude-mythos-5") && !strings.HasPrefix(model, "claude-haiku-5-5") {
			return cap
		}
		cap.Kind, cap.Disable, cap.Manual, cap.MinBudget = "budget", true, true, 1024
		cap.DefaultMode = "disabled"
		switch {
		case strings.HasPrefix(model, "claude-opus-4-5"):
			cap.Levels, cap.Interleaved = []string{"low", "medium", "high"}, true
		case strings.HasPrefix(model, "claude-sonnet-4-5"), strings.HasPrefix(model, "claude-sonnet-4-"), strings.HasPrefix(model, "claude-opus-4-1"):
			cap.Interleaved = true
		}
		switch {
		case strings.HasPrefix(model, "claude-opus-4-6"), strings.HasPrefix(model, "claude-sonnet-4-6"):
			cap.Kind, cap.Levels, cap.DefaultEffort = "adaptive", []string{"low", "medium", "high", "max"}, "high"
			cap.Interleaved = strings.HasPrefix(model, "claude-sonnet-4-6")
		case strings.HasPrefix(model, "claude-opus-4-7"), strings.HasPrefix(model, "claude-opus-4-8"), strings.HasPrefix(model, "claude-opus-5"), strings.HasPrefix(model, "claude-sonnet-5"), strings.HasPrefix(model, "claude-fable-5"), strings.HasPrefix(model, "claude-mythos-5"), strings.HasPrefix(model, "claude-haiku-5-5"):
			cap.Kind, cap.Manual, cap.DefaultMode = "adaptive", false, "auto"
			cap.Levels = []string{"low", "medium", "high", "xhigh", "max"}
			cap.DefaultEffort = "high"
			if strings.HasPrefix(model, "claude-opus-4-") {
				cap.DefaultMode = "disabled"
			}
			if strings.HasPrefix(model, "claude-opus-5") || strings.HasPrefix(model, "claude-haiku-5-5") {
				cap.DisabledLevels = []string{"low", "medium", "high"}
			}
			if strings.HasPrefix(model, "claude-sonnet-5-5") {
				cap.BetweenToolsLevels = []string{"low", "medium", "high"}
			}
			if strings.HasPrefix(model, "claude-haiku-5-5") {
				cap.DefaultEffort = "medium"
			}
			if strings.HasPrefix(model, "claude-opus-5-5") {
				cap.Disable, cap.DefaultEffort = false, "medium"
			}
			if strings.HasPrefix(model, "claude-fable-5") || strings.HasPrefix(model, "claude-mythos-5") || strings.HasPrefix(model, "claude-sonnet-5-5") {
				cap.Disable = false
			}
		}
	case "openai-chat", "responses":
		if strings.Contains(model, "pro") || strings.Contains(model, "codex") {
			return cap
		}
		cap.Source = "https://developers.openai.com/api/docs/guides/reasoning"
		switch {
		case slices.Contains([]string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4o-2024-05-13", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20"}, model):
			cap.Kind, cap.Disable, cap.DefaultMode = "none", true, "disabled"
		case model == "gpt-5.6", strings.HasPrefix(model, "gpt-5.6-sol"), strings.HasPrefix(model, "gpt-6.1-sol"), strings.HasPrefix(model, "gpt-6-astra"), strings.HasPrefix(model, "gpt-6-sol"), strings.HasPrefix(model, "gpt-6-luna"):
			cap.Kind, cap.Levels, cap.DefaultMode, cap.DefaultEffort = "level", []string{"low", "medium", "high", "xhigh", "max"}, "enabled", "medium"
			cap.Disable = model == "gpt-5.6" || strings.HasPrefix(model, "gpt-5.6-sol") || strings.HasPrefix(model, "gpt-6-sol") || strings.HasPrefix(model, "gpt-6-luna")
			cap.MaxOutputTokens = 128000
		case strings.HasPrefix(model, "gpt-5.1"), strings.HasPrefix(model, "gpt-5.2"), strings.HasPrefix(model, "gpt-5.4"), strings.HasPrefix(model, "gpt-5.5"), model == "gpt-5", strings.HasPrefix(model, "gpt-5-"):
			cap.Kind, cap.DefaultMode, cap.DefaultEffort = "level", "enabled", "medium"
			cap.Levels = []string{"minimal", "low", "medium", "high"}
			if strings.HasPrefix(model, "gpt-5.") {
				cap.Disable, cap.DefaultMode = true, "disabled"
				cap.Levels = []string{"low", "medium", "high"}
			}
			if strings.HasPrefix(model, "gpt-5.2") || strings.HasPrefix(model, "gpt-5.4") || strings.HasPrefix(model, "gpt-5.5") {
				cap.Levels = append(cap.Levels, "xhigh")
			}
			if strings.HasPrefix(model, "gpt-5.5") {
				cap.DefaultMode = "enabled"
			}
		case strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
			cap.Kind, cap.Levels, cap.DefaultMode, cap.DefaultEffort = "level", []string{"low", "medium", "high"}, "enabled", "medium"
		}
	}
	return cap
}

// Interleaving is enabled only by the actual outgoing operation's beta header.
func OperationInterleavedThinking(operation Operation) bool {
	for key, value := range operation.Headers {
		if strings.EqualFold(key, "anthropic-beta") {
			for _, flag := range strings.Split(value, ",") {
				if strings.TrimSpace(flag) == "interleaved-thinking-2025-05-14" {
					return true
				}
			}
		}
	}
	return false
}
