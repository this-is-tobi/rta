// Package theme is the single source of truth for styling across every
// renderer (PROJECT.md §8). Sober but warm: one identity color, one sparing
// accent, semantic state colors, and — the detail that makes structure
// recede and content pop — two distinct grays: Faint for chrome (borders,
// fills) and Muted for secondary text. No other renderer defines colors.
//
// Colors are truecolor and degrade automatically: bubbletea v2 downsamples
// TUI output to the terminal's profile, and the CLI wraps stdout/stderr in a
// colorprofile writer. On 256-color terminals every shade has a near match.
package theme

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette.
var (
	Primary = lipgloss.Color("#D97757") // clay orange — identity, keys, selection
	Accent  = lipgloss.Color("#F0A868") // amber — sparing highlights (filter matches)
	Muted   = lipgloss.Color("#8B8B96") // secondary text
	Faint   = lipgloss.Color("#4A4A56") // structure: borders, chart fills
	Good    = lipgloss.Color("#3ED598")
	Warn    = lipgloss.Color("#FFC24B")
	Bad     = lipgloss.Color("#FF6B7A")
	Inverse = lipgloss.Color("#FFFFFF")
	Ink     = lipgloss.Color("#1A1A22")
)

// Shared styles.
var (
	Key       = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	Header    = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	Border    = lipgloss.NewStyle().Foreground(Faint)
	Subtle    = lipgloss.NewStyle().Foreground(Muted)
	Faded     = lipgloss.NewStyle().Foreground(Faint)
	Title     = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	AccentTxt = lipgloss.NewStyle().Foreground(Accent)
	GoodText  = lipgloss.NewStyle().Foreground(Good)
	WarnText  = lipgloss.NewStyle().Foreground(Warn)
	BadText   = lipgloss.NewStyle().Foreground(Bad)
	ErrBadge  = lipgloss.NewStyle().Foreground(Inverse).Background(Bad).Bold(true).Padding(0, 1)
	HintBadge = lipgloss.NewStyle().Foreground(Ink).Background(Warn).Bold(true).Padding(0, 1)
)

// Plain is the zero style, used to switch styling off wholesale.
var Plain = lipgloss.NewStyle()

// StatusKind classifies a status string for semantic coloring.
type StatusKind int

const (
	StatusNeutral StatusKind = iota
	StatusGood
	StatusWarn
	StatusBad
	StatusMuted
)

// ClassifyStatus maps common status vocabulary to a semantic kind. Renderers
// use it to color KindStatus cells; the vocabulary is deliberately small and
// grows only with real usage.
func ClassifyStatus(s string) StatusKind {
	v := strings.ToLower(strings.TrimSpace(s))
	switch {
	case v == "":
		return StatusNeutral
	case strings.HasPrefix(v, "ok") || strings.HasPrefix(v, "valid") ||
		strings.HasPrefix(v, "open") || strings.HasPrefix(v, "up") ||
		strings.HasPrefix(v, "active") || strings.HasPrefix(v, "read"):
		return StatusGood
	case strings.HasPrefix(v, "warn") || strings.HasPrefix(v, "write") ||
		strings.HasPrefix(v, "pending"):
		return StatusWarn
	case strings.HasPrefix(v, "error") || strings.HasPrefix(v, "expired") ||
		strings.HasPrefix(v, "invalid") || strings.HasPrefix(v, "fail") ||
		strings.HasPrefix(v, "denied") || strings.HasPrefix(v, "unreachable") ||
		strings.HasPrefix(v, "destructive") || strings.HasPrefix(v, "down"):
		return StatusBad
	case strings.HasPrefix(v, "closed") || strings.HasPrefix(v, "info") ||
		strings.HasPrefix(v, "none") || strings.HasPrefix(v, "disabled") ||
		strings.HasPrefix(v, "done"):
		return StatusMuted
	default:
		return StatusNeutral
	}
}

// StatusStyle returns the style for a status string.
func StatusStyle(s string) lipgloss.Style {
	switch ClassifyStatus(s) {
	case StatusGood:
		return GoodText
	case StatusWarn:
		return WarnText
	case StatusBad:
		return BadText
	case StatusMuted:
		return Subtle
	default:
		return Plain
	}
}
