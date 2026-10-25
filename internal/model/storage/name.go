package storage

import "strings"

func FoldName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
