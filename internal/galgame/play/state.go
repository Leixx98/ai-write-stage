package play

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/voocel/ainovel-cli/internal/store"
)

const (
	minSpineStations = 3
	maxSpineStations = 6
)

func normalizeFactID(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || unicode.IsSpace(r):
			if b.Len() > 0 {
				b.WriteByte('_')
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func normalizeFacts(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = normalizeFactID(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func applyFacts(ledger store.PlayLedger, ids []string) store.PlayLedger {
	have := map[string]bool{}
	for _, fact := range ledger.Facts {
		have[fact.ID] = true
	}
	for _, id := range normalizeFacts(ids) {
		if have[id] {
			continue
		}
		have[id] = true
		ledger.Facts = append(ledger.Facts, store.PlayFact{ID: id})
	}
	if ledger.Facts == nil {
		ledger.Facts = []store.PlayFact{}
	}
	return ledger
}

func validateSpineStations(stations []store.PlayStation) error {
	if n := len(stations); n < minSpineStations || n > maxSpineStations {
		return fmt.Errorf("spine needs %d to %d stations", minSpineStations, maxSpineStations)
	}
	seen := map[string]bool{}
	for i, station := range stations {
		id := strings.TrimSpace(station.ID)
		pressure := strings.TrimSpace(station.Pressure)
		if id == "" || pressure == "" {
			return fmt.Errorf("stations[%d]: id and pressure are required", i)
		}
		if seen[id] {
			return fmt.Errorf("stations[%d]: duplicate id %q", i, id)
		}
		seen[id] = true
	}
	return nil
}

func prepareSpine(stations []store.PlayStation) store.PlaySpine {
	out := make([]store.PlayStation, 0, len(stations))
	for _, station := range stations {
		status := station.Status
		if status == "" {
			status = store.StationPending
		}
		out = append(out, store.PlayStation{
			ID:       strings.TrimSpace(station.ID),
			Pressure: strings.TrimSpace(station.Pressure),
			Status:   status,
		})
	}
	return store.PlaySpine{Stations: out}
}

func currentStation(spine store.PlaySpine) (store.PlayStation, int, bool) {
	active := -1
	pending := -1
	for i, station := range spine.Stations {
		switch station.Status {
		case store.StationActive:
			if active < 0 {
				active = i
			}
		case store.StationPending, "":
			if pending < 0 {
				pending = i
			}
		}
	}
	idx := active
	if idx < 0 {
		idx = pending
	}
	if idx < 0 {
		return store.PlayStation{}, -1, false
	}
	return spine.Stations[idx], idx, true
}

func remainingStations(spine store.PlaySpine, currentID string) []store.PlayStation {
	out := make([]store.PlayStation, 0, len(spine.Stations))
	for _, station := range spine.Stations {
		if station.ID == currentID {
			continue
		}
		if station.Status == store.StationPending || station.Status == "" {
			out = append(out, station)
		}
	}
	return out
}

func markStation(spine store.PlaySpine, id string, status store.PlayStationStatus) store.PlaySpine {
	for i := range spine.Stations {
		if spine.Stations[i].ID != id {
			continue
		}
		spine.Stations[i].Status = status
	}
	return spine
}

func activateStation(spine store.PlaySpine, id string) store.PlaySpine {
	for i := range spine.Stations {
		switch spine.Stations[i].Status {
		case store.StationDone, store.StationSkipped:
			continue
		case store.StationActive:
			if spine.Stations[i].ID != id {
				spine.Stations[i].Status = store.StationPending
			}
		}
		if spine.Stations[i].ID == id {
			spine.Stations[i].Status = store.StationActive
		}
	}
	return spine
}

func skipPending(spine store.PlaySpine) store.PlaySpine {
	for i := range spine.Stations {
		if spine.Stations[i].Status == store.StationPending || spine.Stations[i].Status == store.StationActive || spine.Stations[i].Status == "" {
			spine.Stations[i].Status = store.StationSkipped
		}
	}
	return spine
}

func spineOpen(spine store.PlaySpine) bool {
	_, _, ok := currentStation(spine)
	return ok
}

func lastOpenStation(spine store.PlaySpine, currentID string) bool {
	return len(remainingStations(spine, currentID)) == 0
}

func similarChoice(history []store.PlayChoiceRecord, cards []store.PlayBeatCard) error {
	if len(history) == 0 || len(cards) == 0 {
		return nil
	}
	last := cards[len(cards)-1]
	if last.Kind != store.BeatChoice {
		return nil
	}
	recent := history
	if len(recent) > 2 {
		recent = recent[len(recent)-2:]
	}
	for _, choice := range last.Choices {
		for _, prev := range recent {
			if strings.TrimSpace(choice.ID) != "" && strings.TrimSpace(choice.ID) == strings.TrimSpace(prev.ChoiceID) {
				return fmt.Errorf("choice %q repeats a recent option", choice.ID)
			}
			if similarLabel(choice.Label, prev.Label) {
				return fmt.Errorf("choice %q repeats recent label %q", choice.Label, prev.Label)
			}
		}
	}
	return nil
}

func similarLabel(a, b string) bool {
	left, right := foldLabel(a), foldLabel(b)
	if left == "" || right == "" || left != right && !strings.Contains(left, right) && !strings.Contains(right, left) {
		return false
	}
	return len(left) >= 4 && len(right) >= 4
}

func foldLabel(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
