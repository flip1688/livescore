package model

import (
	"testing"
	"time"
)

func prizes(pairs ...string) []LottoPrize {
	out := make([]LottoPrize, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, LottoPrize{Type: pairs[i], Number: pairs[i+1]})
	}
	return out
}

func TestIsComplete(t *testing.T) {
	// The Thai government draw publishes two front-three and two last-three
	// numbers; upstream's older rows carry one of each, or none at all.
	full := prizes(
		"first", "645654",
		"front3", "938", "front3", "101",
		"last3", "260", "last3", "775",
		"last2", "23",
	)
	half := prizes("first", "123456", "front3", "123", "last3", "123", "last2", "12")
	firstOnly := prizes("first", "751495")

	tests := []struct {
		name     string
		slug     string
		category string
		canceled bool
		prizes   []LottoPrize
		want     bool
	}{
		{"gov-th full", "gov-th", LottoCatGov, false, full, true},
		{"gov-th one of each", "gov-th", LottoCatGov, false, half, false},
		{"gov-th first only", "gov-th", LottoCatGov, false, firstOnly, false},
		{"gov-th canceled counts as complete", "gov-th", LottoCatGov, true, nil, true},

		// gov-gsb and gov-baac share gov's category but not its vocabulary.
		{"gsb own vocabulary", "gov-gsb", LottoCatGov, false,
			prizes("ticket", "123456", "3up", "123", "2down", "45"), true},
		{"gsb judged by gov-th spec would fail", "gov-gsb", LottoCatGov, false,
			prizes("ticket", "123456", "3up", "123"), false},

		{"hanoi minimum", "hanoi-normal", LottoCatHanoi, false,
			prizes("3up", "384", "2down", "30"), true},
		{"hanoi missing 2down", "hanoi-normal", LottoCatHanoi, false,
			prizes("3up", "384"), false},
		{"stock with optional 4up", "stock-dowjones-vip", LottoCatStock, false,
			prizes("4up", "1234", "3up", "234", "2down", "34"), true},

		{"empty prizes never complete", "gov-th", LottoCatGov, false, nil, false},
		// A blank number is upstream saying "no value", not a prize row.
		{"blank numbers do not count", "hanoi-normal", LottoCatHanoi, false,
			prizes("3up", "", "2down", ""), false},
		// An unknown market has no spec: any prize row at all will do.
		{"unknown market with a prize", "brand-new", "mystery", false,
			prizes("3up", "123"), true},
		{"unknown market with none", "brand-new", "mystery", false, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsComplete(tc.slug, tc.category, tc.canceled, tc.prizes); got != tc.want {
				t.Fatalf("IsComplete = %v, want %v", got, tc.want)
			}
		})
	}
}

func at(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

func TestMarkSupersededKeepsNewestPublish(t *testing.T) {
	// Upstream's real gov-th duplicate: the same 16 Jul draw under two ids,
	// published a day apart.
	draws := []LottoDraw{
		{ID: "525", Slug: "gov-th", DrawDay: "2026-07-16", PublishedAt: at("2026-07-21T16:34:28Z")},
		{ID: "689", Slug: "gov-th", DrawDay: "2026-07-16", PublishedAt: at("2026-07-22T15:41:50Z")},
		{ID: "900", Slug: "gov-th", DrawDay: "2026-08-01", PublishedAt: at("2026-08-01T09:00:00Z")},
	}
	MarkSuperseded(draws)

	bySlug := map[string]bool{}
	for _, d := range draws {
		bySlug[d.ID] = d.Superseded
	}
	if !bySlug["525"] {
		t.Error("older publish (525) should be superseded")
	}
	if bySlug["689"] {
		t.Error("newer publish (689) should survive")
	}
	if bySlug["900"] {
		t.Error("a draw with no duplicate should never be superseded")
	}
}

func TestMarkSupersededIsStableAndIdempotent(t *testing.T) {
	// Equal publish times must not flip between runs, or the visible result
	// would change on every sweep.
	mk := func() []LottoDraw {
		return []LottoDraw{
			{ID: "10", Slug: "gov-th", DrawDay: "2026-07-01", PublishedAt: at("2026-07-21T08:09:21Z")},
			{ID: "557", Slug: "gov-th", DrawDay: "2026-07-01", PublishedAt: at("2026-07-21T08:09:21Z")},
		}
	}
	first := mk()
	MarkSuperseded(first)
	second := mk()
	MarkSuperseded(second)
	MarkSuperseded(second) // running twice must not change the outcome
	for i := range first {
		if first[i].Superseded != second[i].Superseded {
			t.Fatalf("draw %s: unstable superseded flag", first[i].ID)
		}
	}
	if !first[0].Superseded || first[1].Superseded {
		t.Error("on a tie the larger id should win")
	}
}

func TestMarkSupersededSeparatesMarkets(t *testing.T) {
	// Two markets drawing on the same day are not duplicates of each other.
	draws := []LottoDraw{
		{ID: "1", Slug: "hanoi-normal", DrawDay: "2026-11-18", PublishedAt: at("2026-11-18T11:30:00Z")},
		{ID: "2", Slug: "hanoi-vip", DrawDay: "2026-11-18", PublishedAt: at("2026-11-18T12:30:00Z")},
	}
	MarkSuperseded(draws)
	for _, d := range draws {
		if d.Superseded {
			t.Fatalf("%s wrongly superseded across markets", d.Slug)
		}
	}
}

func TestMarkActive(t *testing.T) {
	now := at("2026-11-19T00:00:00Z")
	markets := []LottoMarket{
		{Slug: "hanoi-normal", LatestDrawAt: at("2026-11-18T11:30:00Z")},
		{Slug: "stock-th-noon", LatestDrawAt: at("2026-06-13T05:30:00Z")}, // dead since June
		{Slug: "maekhongvip"}, // never fed a draw
	}
	MarkActive(markets, now, 30*24*time.Hour)

	if !markets[0].Active {
		t.Error("a market that drew yesterday should be active")
	}
	if markets[1].Active {
		t.Error("a market silent for five months should be inactive")
	}
	if markets[2].Active {
		t.Error("a market with no draws at all should be inactive")
	}
}

func TestSortDrawsDesc(t *testing.T) {
	draws := []LottoDraw{
		{ID: "1", DrawAt: at("2026-11-01T09:00:00Z")},
		{ID: "3", DrawAt: at("2026-11-18T09:00:00Z")},
		{ID: "2", DrawAt: at("2026-11-16T09:00:00Z")},
	}
	SortDrawsDesc(draws)
	if draws[0].ID != "3" || draws[1].ID != "2" || draws[2].ID != "1" {
		t.Fatalf("not sorted newest first: %v", []string{draws[0].ID, draws[1].ID, draws[2].ID})
	}
}
