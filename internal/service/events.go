package service

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/flip1688/livescore/internal/thscore"
)

// mergeEvents unions two per-match event lists by EventID. When both carry
// the same EventID, incoming wins — syncEventsStats always calls this with
// incoming = the payload fetched later in the cycle (UTC-today merges over
// UTC-yesterday), so a conflicting copy is resolved in favor of the fresher
// fetch. Order is irrelevant here: callers sort the result afterward.
func mergeEvents(existing, incoming []thscore.EventItem) []thscore.EventItem {
	byID := make(map[int]thscore.EventItem, len(existing)+len(incoming))
	for _, e := range existing {
		byID[e.EventID] = e
	}
	for _, e := range incoming {
		byID[e.EventID] = e
	}
	merged := make([]thscore.EventItem, 0, len(byID))
	for _, e := range byID {
		merged = append(merged, e)
	}
	return merged
}

// eventMinute parses EventItem.Minute ("45", "90", or stoppage-time style
// like "45+2") into a sortable int, taking only the part before "+".
// Unparseable values (malformed payloads) sort last rather than panicking or
// silently sorting to the front.
func eventMinute(raw string) int {
	main, _, _ := strings.Cut(raw, "+")
	n, err := strconv.Atoi(strings.TrimSpace(main))
	if err != nil {
		return math.MaxInt32
	}
	return n
}

// sortEvents orders a match's events by (numeric minute, EventID) so storage
// order — and therefore the WS payload order — is stable across syncs
// regardless of the order thscore or our merge happened to produce.
func sortEvents(events []thscore.EventItem) {
	sort.Slice(events, func(i, j int) bool {
		mi, mj := eventMinute(events[i].Minute), eventMinute(events[j].Minute)
		if mi != mj {
			return mi < mj
		}
		return events[i].EventID < events[j].EventID
	})
}

// eventsDigest builds a deterministic fingerprint of a match's event list so
// syncEventsStats can tell "nothing changed" apart from a real update.
// Full-day fetching returns every in-play match's complete event list every
// cycle, so without this check we'd re-publish unchanged events to WS
// subscribers once a minute (docs/widgets-repo-analysis.md's documented
// lesson: publish on-change only). OprTime is deliberately excluded — it's
// thscore's own record-modified timestamp and can tick without any field
// here actually changing.
func eventsDigest(events []thscore.EventItem) string {
	sorted := append([]thscore.EventItem(nil), events...)
	sortEvents(sorted)
	var b strings.Builder
	for _, e := range sorted {
		fmt.Fprintf(&b, "%d|%s|%d|%v|%v|%s|%v|%s;",
			e.EventID, e.Minute, e.Type, e.HomeEvent, e.PlayerID, e.PlayerName, e.AssistPlayerID, e.Overtime)
	}
	return b.String()
}
