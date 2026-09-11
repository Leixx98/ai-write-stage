package play

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Leixx98/ai-write-stage/internal/store"
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
		if err := validateStationDetail(station, i); err != nil {
			return err
		}
		id := strings.TrimSpace(station.ID)
		if seen[id] {
			return fmt.Errorf("stations[%d]: duplicate id %q", i, id)
		}
		seen[id] = true
	}
	return nil
}

func validateSpine(spine store.PlaySpine) error {
	if strings.TrimSpace(spine.Throughline) == "" {
		return fmt.Errorf("throughline is required")
	}
	if err := validateSpineStations(spine.Stations); err != nil {
		return err
	}
	return validateThreads(spine.Threads)
}

func validateStationDetail(station store.PlayStation, i int) error {
	id := strings.TrimSpace(station.ID)
	pressure := strings.TrimSpace(station.Pressure)
	if id == "" || pressure == "" {
		return fmt.Errorf("stations[%d]: id and pressure are required", i)
	}
	if strings.TrimSpace(station.Title) == "" {
		return fmt.Errorf("stations[%d]: title is required", i)
	}
	if strings.TrimSpace(station.Summary) == "" {
		return fmt.Errorf("stations[%d]: summary is required", i)
	}
	if len(nonEmptyStrings(station.MustHappen)) == 0 {
		return fmt.Errorf("stations[%d]: must_happen is required", i)
	}
	if n := len(station.Forks); n < 2 || n > 3 {
		return fmt.Errorf("stations[%d]: forks needs 2 or 3 items", i)
	}
	for j, fork := range station.Forks {
		if strings.TrimSpace(fork.Tint) == "" {
			return fmt.Errorf("stations[%d].forks[%d]: tint is required", i, j)
		}
	}
	return nil
}

func validateThreads(threads []store.PlayThread) error {
	if len(threads) == 0 {
		return fmt.Errorf("threads cannot be empty")
	}
	seen := map[string]bool{}
	for i, thread := range threads {
		id := strings.TrimSpace(thread.ID)
		if id == "" || strings.TrimSpace(thread.Hint) == "" {
			return fmt.Errorf("threads[%d]: id and hint are required", i)
		}
		if seen[id] {
			return fmt.Errorf("threads[%d]: duplicate id %q", i, id)
		}
		seen[id] = true
		if err := validateThreadStatus(thread.Status); err != nil {
			return fmt.Errorf("threads[%d]: %w", i, err)
		}
	}
	return nil
}

func validateThreadUpdates(threads []store.PlayThread) error {
	seen := map[string]bool{}
	for i, thread := range threads {
		id := strings.TrimSpace(thread.ID)
		if id == "" {
			return fmt.Errorf("threads[%d]: id is required", i)
		}
		if seen[id] {
			return fmt.Errorf("threads[%d]: duplicate id %q", i, id)
		}
		seen[id] = true
		if thread.Status != "" {
			if err := validateThreadStatus(thread.Status); err != nil {
				return fmt.Errorf("threads[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func validateThreadStatus(status store.PlayThreadStatus) error {
	switch status {
	case "", store.ThreadOpen, store.ThreadPlanted, store.ThreadPaid, store.ThreadDropped:
		return nil
	default:
		return fmt.Errorf("invalid status %q", status)
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func prepareSpine(out SpineOutput) store.PlaySpine {
	stations := make([]store.PlayStation, 0, len(out.Stations))
	for _, station := range out.Stations {
		status := station.Status
		if status == "" {
			status = store.StationPending
		}
		stations = append(stations, store.PlayStation{
			ID:         strings.TrimSpace(station.ID),
			Title:      strings.TrimSpace(station.Title),
			Pressure:   strings.TrimSpace(station.Pressure),
			Summary:    strings.TrimSpace(station.Summary),
			MustHappen: nonEmptyStrings(station.MustHappen),
			Forks:      prepareForks(station.Forks),
			Seeds:      nonEmptyStrings(station.Seeds),
			Payoffs:    nonEmptyStrings(station.Payoffs),
			Status:     status,
		})
	}
	return store.PlaySpine{
		Throughline: strings.TrimSpace(out.Throughline),
		Stations:    stations,
		Threads:     prepareThreads(out.Threads),
	}
}

func prepareForks(forks []store.PlayFork) []store.PlayFork {
	out := make([]store.PlayFork, 0, len(forks))
	for _, fork := range forks {
		out = append(out, store.PlayFork{
			IfFacts: normalizeFacts(fork.IfFacts),
			Tint:    strings.TrimSpace(fork.Tint),
		})
	}
	return out
}

func prepareThreads(threads []store.PlayThread) []store.PlayThread {
	out := make([]store.PlayThread, 0, len(threads))
	seen := map[string]bool{}
	for _, thread := range threads {
		id := strings.TrimSpace(thread.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		status := thread.Status
		if status == "" {
			status = store.ThreadOpen
		}
		out = append(out, store.PlayThread{
			ID:       id,
			Hint:     strings.TrimSpace(thread.Hint),
			Status:   status,
			PlantAt:  strings.TrimSpace(thread.PlantAt),
			PayoffAt: strings.TrimSpace(thread.PayoffAt),
		})
	}
	if out == nil {
		out = []store.PlayThread{}
	}
	return out
}

func applyThreadUpdates(existing []store.PlayThread, updates []store.PlayThread) []store.PlayThread {
	if existing == nil {
		existing = []store.PlayThread{}
	}
	index := map[string]int{}
	for i, thread := range existing {
		index[thread.ID] = i
	}
	for _, update := range prepareThreads(updates) {
		if i, ok := index[update.ID]; ok {
			if update.Status != "" {
				existing[i].Status = update.Status
			}
			if update.Hint != "" {
				existing[i].Hint = update.Hint
			}
			if update.PlantAt != "" {
				existing[i].PlantAt = update.PlantAt
			}
			if update.PayoffAt != "" {
				existing[i].PayoffAt = update.PayoffAt
			}
			continue
		}
		index[update.ID] = len(existing)
		existing = append(existing, update)
	}
	return existing
}

func applyStationDetail(dst *store.PlayStation, src store.PlayStation) {
	if dst == nil {
		return
	}
	prepared := prepareSpine(SpineOutput{Stations: []store.PlayStation{src}})
	if len(prepared.Stations) == 0 {
		return
	}
	next := prepared.Stations[0]
	dst.Title = next.Title
	dst.Pressure = next.Pressure
	dst.Summary = next.Summary
	dst.MustHappen = next.MustHappen
	dst.Forks = next.Forks
	dst.Seeds = next.Seeds
	dst.Payoffs = next.Payoffs
}

func nextPendingStation(spine store.PlaySpine) (store.PlayStation, int, bool) {
	for i, station := range spine.Stations {
		if station.Status == store.StationPending || station.Status == "" {
			return station, i, true
		}
	}
	return store.PlayStation{}, -1, false
}

func firstSkippedIndex(spine store.PlaySpine) int {
	for i, station := range spine.Stations {
		if station.Status == store.StationSkipped {
			return i
		}
	}
	return -1
}

func replanFromIndex(spine store.PlaySpine, awaitingChoice bool) int {
	_, idx, ok := currentStation(spine)
	if ok && awaitingChoice {
		if idx+1 < len(spine.Stations) {
			return idx + 1
		}
		return firstSkippedIndex(spine)
	}
	if ok {
		return idx
	}
	return firstSkippedIndex(spine)
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
