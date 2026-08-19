package llmcontract

import (
	"strings"
	"unicode/utf8"
)

const (
	repairBOM           = "bom"
	repairMarkdownFence = "markdown_fence"
	repairSmartQuotes   = "smart_quotes"
	repairComments      = "comments"
	repairTrailingComma = "trailing_comma"
)

const (
	leftDoubleQuote  = '\u201c'
	rightDoubleQuote = '\u201d'
)

// Repair is a successful conservative syntactic cleanup of model JSON.
type Repair struct {
	Rules     []string
	RawChars  int
	BodyChars int
}

// RepairJSON extracts the first JSON object after conservative syntactic cleanup.
// It never inserts keys, closes truncated strings, or completes missing brackets.
func RepairJSON(raw string) (string, []string) {
	text := strings.TrimSpace(raw)
	var rules []string
	if strings.HasPrefix(text, "\ufeff") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
		rules = appendRule(rules, repairBOM)
	}
	if unwrapped, ok := unwrapMarkdownFence(text); ok {
		text = unwrapped
		rules = appendRule(rules, repairMarkdownFence)
	}
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", rules
	}
	chunk := text[start:]
	cleaned, extra := sanitizeJSONText(chunk)
	body := ExtractJSONObject(cleaned)
	if body == "" {
		return ExtractJSONObject(chunk), rules
	}
	return body, append(rules, extra...)
}

func unwrapMarkdownFence(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "```"))
	if len(rest) >= 4 && strings.EqualFold(rest[:4], "json") {
		rest = rest[4:]
	}
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), true
}

func sanitizeJSONText(s string) (string, []string) {
	var b strings.Builder
	b.Grow(len(s))
	var rules []string
	inString, smartString, escaped := false, false, false
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if inString {
			if escaped {
				b.WriteString(s[i : i+size])
				escaped = false
				i += size
				continue
			}
			if r == '\\' {
				b.WriteByte('\\')
				escaped = true
				i += size
				continue
			}
			if r == '"' || (smartString && isSmartDoubleQuote(r)) {
				if r != '"' {
					rules = appendRule(rules, repairSmartQuotes)
				}
				b.WriteByte('"')
				inString, smartString = false, false
				i += size
				continue
			}
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		if r == '"' || isSmartDoubleQuote(r) {
			if isSmartDoubleQuote(r) {
				rules = appendRule(rules, repairSmartQuotes)
				smartString = true
			}
			b.WriteByte('"')
			inString = true
			escaped = false
			i += size
			continue
		}
		if r == '/' && i+1 < len(s) {
			switch s[i+1] {
			case '/':
				nl := strings.IndexByte(s[i+2:], '\n')
				rules = appendRule(rules, repairComments)
				if nl < 0 {
					return b.String(), rules
				}
				i += 2 + nl
				continue
			case '*':
				end := strings.Index(s[i+2:], "*/")
				if end < 0 {
					return s, nil
				}
				rules = appendRule(rules, repairComments)
				i += 2 + end + 2
				continue
			}
		}
		if r == ',' {
			j := skipSpaceAndComments(s, i+size)
			if j < 0 {
				return s, nil
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				rules = appendRule(rules, repairTrailingComma)
				i += size
				continue
			}
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	if inString {
		return s, nil
	}
	return b.String(), rules
}

func skipSpaceAndComments(s string, i int) int {
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '/' {
			nl := strings.IndexByte(s[i+2:], '\n')
			if nl < 0 {
				return len(s)
			}
			i += 2 + nl
			continue
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return -1
			}
			i += 2 + end + 2
			continue
		}
		return i
	}
	return i
}

func isSmartDoubleQuote(r rune) bool {
	return r == leftDoubleQuote || r == rightDoubleQuote
}

func appendRule(rules []string, rule string) []string {
	for _, existing := range rules {
		if existing == rule {
			return rules
		}
	}
	return append(rules, rule)
}
