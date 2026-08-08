package helper

import "strings"

func IsFlareTimeout(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Error solving the challenge") || strings.Contains(s, "Timeout after")
}
