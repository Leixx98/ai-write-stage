package play

import (
	"strings"
	"unicode"

	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/voocel/agentcore"
)

const defaultWriterWindow = 200000
const writerPromptSafety = 768

func (g Generator) loadWriterSession() (store.PlayWriterSession, error) {
	if g.Store == nil || strings.TrimSpace(g.PlayID) == "" {
		return store.PlayWriterSession{}, nil
	}
	return g.Store.LoadWriterSession(g.PlayID)
}

func (g Generator) saveWriterSession(session store.PlayWriterSession) error {
	if g.Store == nil || strings.TrimSpace(g.PlayID) == "" {
		return nil
	}
	return g.Store.SaveWriterSession(g.PlayID, session)
}

func writerHistoryMessages(turns []store.PlayWriterTurn) []agentcore.Message {
	out := make([]agentcore.Message, 0, len(turns)*2)
	for _, turn := range turns {
		payload, err := marshalTurn(writerTurn{Card: turn.Card})
		if err != nil {
			continue
		}
		reply, err := marshalTurn(WriterOutput{Speaker: turn.Speaker, Text: turn.Text})
		if err != nil {
			continue
		}
		out = append(out, agentcore.UserMsg(payload), agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(reply)},
		})
	}
	return out
}

func capWriterTurns(turns []store.PlayWriterTurn, max int) []store.PlayWriterTurn {
	if max <= 0 || len(turns) <= max {
		return turns
	}
	return turns[len(turns)-max:]
}

func compactWriterTurns(turns []store.PlayWriterTurn, system, payload string, window int) []store.PlayWriterTurn {
	budget := writerHistoryBudget(window, system, payload)
	if writerTurnsFit(turns, budget) {
		return turns
	}
	keep := budget * 3 / 4
	if keep < 1 {
		keep = budget
	}
	for len(turns) > 0 && !writerTurnsFit(turns, keep) {
		turns = turns[1:]
	}
	return turns
}

func writerHistoryBudget(window int, system, payload string) int {
	if window <= 0 {
		window = defaultWriterWindow
	}
	input := max(2048, window-playMaxTokens-writerPromptSafety)
	return max(0, input-estimateTokens(system)-estimateTokens(payload)-48)
}

func writerTurnsFit(turns []store.PlayWriterTurn, budget int) bool {
	return estimateTokens(writerTurnsText(turns)) <= budget
}

func writerTurnsText(turns []store.PlayWriterTurn) string {
	var b strings.Builder
	for _, msg := range writerHistoryMessages(turns) {
		b.WriteString(msg.TextContent())
	}
	return b.String()
}

func historyChars(history []agentcore.Message) int {
	n := 0
	for _, msg := range history {
		n += len([]rune(msg.TextContent()))
	}
	return n
}

func estimateTokens(value string) int {
	ascii := 0
	wide := 0
	for _, r := range value {
		if r <= unicode.MaxASCII {
			ascii++
		} else {
			wide++
		}
	}
	return wide + (ascii+3)/4
}
