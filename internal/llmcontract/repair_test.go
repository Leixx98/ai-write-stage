package llmcontract

import (
	"strings"
	"testing"
)

func TestRepairJSONTrailingCommaAndComments(t *testing.T) {
	raw := "说明\n{\"action\":\"a\",\"reason\":\"ok\", /* keep */\n// line\n}"
	body, rules := RepairJSON(raw)
	if err := ValidateJSON(testContract().Schema, []byte(body)); err != nil {
		t.Fatalf("body=%q rules=%v err=%v", body, rules, err)
	}
	if strings.Contains(body, ",}") || strings.Contains(body, "keep") || strings.Contains(body, "line") {
		t.Fatalf("cleanup incomplete body=%q", body)
	}
	if !hasRule(rules, repairTrailingComma) || !hasRule(rules, repairComments) {
		t.Fatalf("rules=%v", rules)
	}
}

func TestRepairJSONPreservesStringContents(t *testing.T) {
	raw := `{"action":"a","reason":"http://x 他说“你好”, still"}`
	body, rules := RepairJSON(raw)
	if body != raw {
		t.Fatalf("string content changed: %q rules=%v", body, rules)
	}
	if hasRule(rules, repairComments) || hasRule(rules, repairTrailingComma) {
		t.Fatalf("false positive rules=%v", rules)
	}
}

func TestRepairJSONSmartQuoteDelimiters(t *testing.T) {
	raw := `{“action”:“a”,“reason”:“ok”}`
	body, rules := RepairJSON(raw)
	if err := ValidateJSON(testContract().Schema, []byte(body)); err != nil {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if !hasRule(rules, repairSmartQuotes) {
		t.Fatalf("rules=%v", rules)
	}
}

func TestRepairJSONDoesNotCloseTruncation(t *testing.T) {
	raw := `{"action":"a","reason":"ok"`
	body, _ := RepairJSON(raw)
	if body != "" {
		t.Fatalf("truncated JSON should stay unusable, body=%q", body)
	}
}

func TestRepairJSONMarkdownFence(t *testing.T) {
	raw := "```json\n{\"action\":\"a\",\"reason\":\"ok\",}\n```"
	body, rules := RepairJSON(raw)
	if err := ValidateJSON(testContract().Schema, []byte(body)); err != nil {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if !hasRule(rules, repairMarkdownFence) || !hasRule(rules, repairTrailingComma) {
		t.Fatalf("rules=%v", rules)
	}
}

func hasRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}
	return false
}
