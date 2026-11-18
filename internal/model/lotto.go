package model

import (
	"sort"
	"time"
)

// Lotto domain types. The upstream feed is lotto2news (see internal/lotto);
// everything here is already normalized and is what the REST API serves.
//
// Vocabulary note: upstream calls a market a "lottery" and keys it by slug
// (e.g. "gov-th", "hanoi-normal"). We keep the slug as the document _id, the
// same way thscore ids are used for leagues and matches.

// Lotto market categories, verbatim from the upstream /lotteries feed.
//
// There is deliberately no "yeekee" (หวยยี่กี) or "other": the upstream feed
// carries neither — all 162 markets fall into the four below — so a UI
// category for them would always be empty.
const (
	LottoCatGov   = "gov"
	LottoCatLao   = "lao"
	LottoCatHanoi = "hanoi"
	LottoCatStock = "stock"
)

// LottoMarket is one lottery market. Synced daily from upstream's market
// list; Active and LatestDrawAt are ours, not upstream's.
type LottoMarket struct {
	Slug         string `bson:"_id" json:"slug"`
	Name         string `bson:"name" json:"name"`
	ShortName    string `bson:"short_name,omitempty" json:"short_name,omitempty"`
	Category     string `bson:"category" json:"category"`
	Tier         string `bson:"tier,omitempty" json:"tier,omitempty"`
	Country      string `bson:"country,omitempty" json:"country,omitempty"`
	DrawSchedule string `bson:"draw_schedule,omitempty" json:"draw_schedule,omitempty"`

	// LatestDrawAt is the newest draw we hold for this market, stamped by the
	// results sync. Zero means we have no draws at all.
	LatestDrawAt time.Time `bson:"latest_draw_at,omitempty" json:"latest_draw_at,omitempty"`

	// Active is false for markets upstream still lists but no longer feeds.
	// Fourteen markets (the whole แม่โขง family, พม่า, ฮานอยดานัง,
	// ดาวโจนส์ VIP พิเศษ) have never returned a single draw, and หุ้นไทยเที่ยง
	// stopped in June 2026 — upstream flags none of them, so a UI that renders
	// the raw market list gets menu entries that open onto nothing.
	// Computed by MarkActive, not by upstream.
	Active bool `bson:"active" json:"active"`

	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LottoPrize is one prize row. Type is upstream's machine name and differs
// per market family (see LottoPrizeSpec); Label is upstream's Thai display
// text, passed through so the UI never has to translate a type it has not
// seen before.
type LottoPrize struct {
	Type   string `bson:"type" json:"type"`
	Label  string `bson:"label" json:"label"`
	Number string `bson:"number" json:"number"`
}

// LottoDraw is one draw of one market.
type LottoDraw struct {
	// ID is upstream's drawId as a string. It is NOT unique per
	// (market, date): upstream has published the same draw twice under
	// different ids — see Superseded.
	ID       string `bson:"_id" json:"id"`
	Slug     string `bson:"slug" json:"slug"`
	Category string `bson:"category" json:"category"`

	DrawAt time.Time `bson:"draw_at" json:"draw_at"`
	// DrawDay is DrawAt as a Bangkok calendar date ("2026-11-16"), the key
	// the UI groups and paginates by. Unlike football's match_date there is
	// no 04:00 cutover here: a lottery draw belongs to the day it is drawn.
	DrawDay string `bson:"draw_day" json:"draw_day"`

	Round       *int         `bson:"round,omitempty" json:"round,omitempty"`
	Status      string       `bson:"status" json:"status"`
	IsCanceled  bool         `bson:"is_canceled" json:"is_canceled"`
	PublishedAt time.Time    `bson:"published_at,omitempty" json:"published_at,omitempty"`
	Prizes      []LottoPrize `bson:"prizes" json:"prizes"`

	// Complete reports whether Prizes holds every prize row this market's
	// family is supposed to carry (LottoPrizeSpec). Incomplete draws are
	// stored but hidden from the read API: upstream's older government draws
	// routinely carry only รางวัลที่ 1, and a page showing one prize out of
	// four reads as our bug rather than as missing upstream data.
	Complete bool `bson:"complete" json:"complete"`

	// Superseded marks the losing row when upstream has published the same
	// (Slug, DrawDay) more than once. The surviving row is the one with the
	// newest PublishedAt — see MarkSuperseded.
	Superseded bool `bson:"superseded" json:"superseded"`

	FetchedAt time.Time `bson:"fetched_at" json:"fetched_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// LottoPrizeSpec is the minimum prize set a complete draw must carry, keyed
// by market slug and falling back to category. Counts are exact for the Thai
// government draw (two front-three and two last-three numbers are drawn) and
// a floor elsewhere.
//
// Observed over all 162 markets and 13,210 draws on 19 Nov 2026:
//
//	gov-th              first, front3 x2, last3 x2, last2
//	gov-gsb, gov-baac   ticket, 3up, 2down      <- different vocabulary
//	lao/hanoi/stock     3up, 2down (+ optional 4up)
//
// Note that gov-gsb and gov-baac share a category with gov-th but not a
// prize vocabulary, so rendering must branch on the spec, never on Category.
var LottoPrizeSpec = map[string]map[string]int{
	"gov-th":   {"first": 1, "front3": 2, "last3": 2, "last2": 1},
	"gov-gsb":  {"ticket": 1, "3up": 1, "2down": 1},
	"gov-baac": {"ticket": 1, "3up": 1, "2down": 1},

	LottoCatLao:   {"3up": 1, "2down": 1},
	LottoCatHanoi: {"3up": 1, "2down": 1},
	LottoCatStock: {"3up": 1, "2down": 1},
}

// PrizeSpecFor resolves the spec for a market, slug first then category.
// An unknown market yields nil, which IsComplete treats as "no requirement".
func PrizeSpecFor(slug, category string) map[string]int {
	if spec, ok := LottoPrizeSpec[slug]; ok {
		return spec
	}
	return LottoPrizeSpec[category]
}

// IsComplete reports whether prizes satisfy the market's spec. A canceled
// draw is complete by definition: there are no numbers to carry.
func IsComplete(slug, category string, isCanceled bool, prizes []LottoPrize) bool {
	if isCanceled {
		return true
	}
	spec := PrizeSpecFor(slug, category)
	if len(spec) == 0 {
		return len(prizes) > 0
	}
	counts := make(map[string]int, len(prizes))
	for _, p := range prizes {
		if p.Number != "" {
			counts[p.Type]++
		}
	}
	for typ, want := range spec {
		if counts[typ] < want {
			return false
		}
	}
	return true
}

// MarkSuperseded sets Superseded on every draw that upstream has replaced:
// within one (Slug, DrawDay) only the newest PublishedAt survives. Ties fall
// back to the larger ID, so the choice is stable across runs.
//
// Upstream really does this — gov-th's 16 Jul 2026 draw exists as both
// drawId 525 and 689 with identical numbers, and its 1 Jul 2026 draw exists
// as both a real result and a leftover test row (first=123456). Deduplicating
// on write rather than on read keeps the index on (slug, superseded,
// draw_at) usable for the history endpoint.
func MarkSuperseded(draws []LottoDraw) {
	type key struct{ slug, day string }
	best := make(map[key]int, len(draws))
	for i := range draws {
		draws[i].Superseded = false
		k := key{draws[i].Slug, draws[i].DrawDay}
		j, seen := best[k]
		if !seen || newerDraw(draws[i], draws[j]) {
			best[k] = i
		}
	}
	keep := make(map[int]bool, len(best))
	for _, i := range best {
		keep[i] = true
	}
	for i := range draws {
		if !keep[i] {
			draws[i].Superseded = true
		}
	}
}

func newerDraw(a, b LottoDraw) bool {
	if !a.PublishedAt.Equal(b.PublishedAt) {
		return a.PublishedAt.After(b.PublishedAt)
	}
	return a.ID > b.ID
}

// MarkActive flags markets that have produced a draw within staleAfter of
// now. Upstream keeps dead markets in its list forever with no indication,
// so this is the only signal we have — see LottoMarket.Active.
func MarkActive(markets []LottoMarket, now time.Time, staleAfter time.Duration) {
	cutoff := now.Add(-staleAfter)
	for i := range markets {
		markets[i].Active = !markets[i].LatestDrawAt.IsZero() &&
			markets[i].LatestDrawAt.After(cutoff)
	}
}

// SortDrawsDesc orders draws newest first, the order every read path wants.
func SortDrawsDesc(draws []LottoDraw) {
	sort.SliceStable(draws, func(i, j int) bool {
		if !draws[i].DrawAt.Equal(draws[j].DrawAt) {
			return draws[i].DrawAt.After(draws[j].DrawAt)
		}
		return draws[i].ID > draws[j].ID
	})
}
