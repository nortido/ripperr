package runner

import (
	"bytes"
	"regexp"
	"strings"

	"ripperr/internal/helper"
)

const pageSampleMaxRunes = 500

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe       = regexp.MustCompile(`\s+`)
)

func pageSample(b *bytes.Buffer, encoded bool) string {
	text := helper.PageText(b, encoded)
	text = scriptStyleRe.ReplaceAllString(text, " ")
	text = tagRe.ReplaceAllString(text, " ")
	text = spaceRe.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > pageSampleMaxRunes {
		runes = runes[:pageSampleMaxRunes]
	}
	return string(runes)
}
