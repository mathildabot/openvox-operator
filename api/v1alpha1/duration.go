package v1alpha1

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseDurationToSeconds parses a human-readable duration string into seconds.
// Supported units: s (seconds), m (minutes), h (hours), d (days), y (years).
// Plain numbers without a unit are interpreted as seconds.
func ParseDurationToSeconds(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Plain number = seconds
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v, nil
	}

	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration: %q", s)
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %q", s)
	}

	switch unit {
	case 's':
		return num, nil
	case 'm':
		return num * 60, nil
	case 'h':
		return num * 3600, nil
	case 'd':
		return num * 86400, nil
	case 'y':
		return num * 365 * 86400, nil
	default:
		return 0, fmt.Errorf("unknown duration unit %q in %q (supported: s, m, h, d, y)", string(unit), s)
	}
}
