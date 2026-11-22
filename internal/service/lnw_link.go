package service

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/flip1688/livescore/internal/model"
)

// Crosswalk between the lnwthailive feeds and thscore.
//
// Upstream provides no join key of any kind: its fixture feed, result feed
// and analysis feed each use their own id space, and none of them is
// thscore's. Every link below is therefore inferred, and every link records
// how it was inferred so a reader can discount it. Nothing here writes an
// inferred id back into a feed document.
//
// The honest finding of this package is as much the links it fails to make
// as the ones it makes: an aggregator that cannot be joined is more
// expensive to adopt than one that can, and LnwCoverage reports both.

// linkWindow is how far two kick-off times may differ and still be taken
// for the same match. Feeds round kick-off differently and a postponement
// of a few minutes is normal; beyond this they are different matches.
const linkWindow = 90 * time.Minute

// scoreWindow is the tighter tolerance used once names are out of the
// picture. At 90 minutes almost every fixture of the day is a candidate;
// at five, kick-off is discriminating enough to be worth something.
const scoreWindow = 5 * time.Minute

// hasThai reports whether a name is written in Thai at all.
//
// The fixture feed mixes scripts inside a single row — one fixture on
// 2026-11-22 read home "arezzo", away "ปาโดวา" — and thscore names every
// team in Thai. A Latin name therefore has nothing to be compared against,
// which is a different kind of failure from "compared and disagreed" and
// is counted separately.
func hasThai(s string) bool {
	for _, r := range s {
		if r >= 0x0E00 && r <= 0x0E7F {
			return true
		}
	}
	return false
}

// normalizeTeam reduces a team name to something comparable across feeds:
// case-folded, punctuation and spacing removed, and the club-type words
// that one feed includes and another omits stripped.
//
// It does not translate. The fixture feed names teams in Thai and the
// result feed in English, so those two can never be matched by name — only
// by time — and that gap is reported rather than papered over.
func normalizeTeam(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, w := range []string{
		"football club", "fc", "cf", "sc", "ac", "afc", "cd", "ud", "sv",
		"เอฟซี", "สโมสร", "ทีม",
	} {
		s = strings.ReplaceAll(s, w, "")
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// linkID keys a link by the pairing itself rather than by any feed's id,
// so the same fixture seen through different feeds converges on one row.
func linkID(day, home, away string) string {
	return day + "|" + normalizeTeam(home) + "|" + normalizeTeam(away)
}

// SyncCoverage recomputes the vendor comparison for the current matchdate
// and the previous one.
//
// It reads only what the sync loops have already stored, so it spends no
// upstream quota and can be re-run freely. The previous day is included
// because results keep arriving after midnight.
func (s *LnwSyncer) SyncCoverage(ctx context.Context) error {
	now := time.Now()
	days := []string{
		CurrentMatchDate(now),
		CurrentMatchDate(now.AddDate(0, 0, -1)),
	}
	for _, day := range days {
		cov, links, err := s.computeCoverage(ctx, day)
		if err != nil {
			return err
		}
		if err := s.store.UpsertLnwLinks(ctx, links); err != nil {
			return err
		}
		if err := s.store.SaveLnwCoverage(ctx, cov); err != nil {
			return err
		}
		s.log.Info("lnw coverage computed",
			"day", day,
			"lnw_matches", cov.LnwMatches, "thscore_matches", cov.ThscoreMatches,
			"linked", cov.Linked, "by_name", cov.LinkedByName,
			"by_score", cov.LinkedByScore, "fuzzy", cov.LinkedFuzzy,
			"only_lnw", cov.OnlyLnw, "only_thscore", cov.OnlyThscore,
			"unlinked_script", cov.UnlinkedScript,
			"unlinked_ambiguous", cov.UnlinkedAmbiguous,
			"unlinked_no_candidate", cov.UnlinkedNoCandidate,
			"lnw_results", cov.LnwResults, "with_stream", cov.LnwWithStream)
	}
	return nil
}

// computeCoverage builds one day's comparison and the links behind it.
func (s *LnwSyncer) computeCoverage(ctx context.Context, day string) (model.LnwCoverage, []model.LnwMatchLink, error) {
	cov := model.LnwCoverage{Day: day}

	lnwMatches, err := s.store.LnwMatchesByDate(ctx, day)
	if err != nil {
		return cov, nil, err
	}
	results, err := s.store.LnwResultsByDate(ctx, day)
	if err != nil {
		return cov, nil, err
	}
	analyses, err := s.store.LnwAnalysisByDate(ctx, day)
	if err != nil {
		return cov, nil, err
	}
	thMatches, err := s.store.ListMatchesByMatchDate(ctx, day)
	if err != nil {
		return cov, nil, err
	}
	thCount, thLeagues, err := s.store.CountMatchesByDate(ctx, day)
	if err != nil {
		return cov, nil, err
	}

	cov.LnwMatches = len(lnwMatches)
	cov.LnwResults = len(results)
	cov.LnwAnalysis = len(analyses)
	cov.ThscoreMatches = thCount
	cov.ThscoreLeagues = thLeagues

	leagues := map[string]struct{}{}
	for _, m := range lnwMatches {
		leagues[m.LeagueName] = struct{}{}
		if len(m.Channels) > 0 {
			cov.LnwWithStream++
		}
	}
	cov.LnwLeagues = len(leagues)

	now := time.Now().UTC()
	byKey := map[string]*model.LnwMatchLink{}
	get := func(id string) *model.LnwMatchLink {
		if l, ok := byKey[id]; ok {
			return l
		}
		l := &model.LnwMatchLink{ID: id, MatchDate: day, UpdatedAt: now}
		byKey[id] = l
		return l
	}

	// The vendor's own fixture feed is the anchor: it is the one with Thai
	// names and a live score, i.e. the one that would actually replace
	// thscore on the page.
	for _, m := range lnwMatches {
		l := get(linkID(day, m.HomeName, m.AwayName))
		l.MatchID = m.ID
		l.Method = model.LnwLinkExactName
		l.Confidence = 1
	}

	// thscore: Thai names on both sides, so a name match is meaningful.
	// A kick-off far outside the window rejects the pairing — two different
	// fixtures between the same clubs on one day do happen.
	for _, m := range thMatches {
		id := linkID(day, m.HomeName, m.AwayName)
		if l, ok := byKey[id]; ok {
			if lm := findMatchByID(lnwMatches, l.MatchID); lm != nil &&
				absDuration(lm.KickoffAt.Sub(m.KickoffAt)) > linkWindow {
				continue
			}
			l.ThscoreID = m.ID
			cov.LinkedByName++
			continue
		}
		l := get(id)
		l.ThscoreID = m.ID
		l.Method = model.LnwLinkExactName
		l.Confidence = 1
	}

	// The analysis feed is Thai too, so the same name match applies.
	for _, a := range analyses {
		id := linkID(day, a.HomeName, a.AwayName)
		if l, ok := byKey[id]; ok {
			l.AnalysisID = a.ID
			continue
		}
		l := get(id)
		l.AnalysisID = a.ID
		l.Method = model.LnwLinkExactName
		l.Confidence = 1
	}

	// Everything still unlinked has failed on names. Kick-off time and the
	// score are the only signals left, and they are weak enough that this
	// pass refuses far more often than it links — by design.
	linkRemainingByTime(lnwMatches, thMatches, byKey, day, now, &cov)

	// The result feed names teams in English while everything else is in
	// Thai, so there is no name to match on. The only remaining signal is
	// kick-off time, and it is only trustworthy when exactly one candidate
	// sits in the window — a weaker link, recorded as such.
	linkResultsByTime(results, lnwMatches, byKey, day, now, &cov)

	links := make([]model.LnwMatchLink, 0, len(byKey))
	for _, l := range byKey {
		links = append(links, *l)
		switch {
		case l.MatchID != "" && l.ThscoreID != "":
			cov.Linked++
		case l.MatchID != "":
			cov.OnlyLnw++
		case l.ThscoreID != "":
			cov.OnlyThscore++
		}
		switch l.Method {
		case model.LnwLinkTimeWindow, model.LnwLinkTimeScore, model.LnwLinkFuzzyName:
			cov.LinkedFuzzy++
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].ID < links[j].ID })
	return cov, links, nil
}

// linkRemainingByTime links fixtures that no name comparison could reach.
//
// It runs after the name passes and considers only lnw rows that still have
// no thscore id. A candidate must kick off within scoreWindow; when several
// do, an identical score breaks the tie, and when that too is ambiguous the
// pairing is left unmade and counted in cov.UnlinkedAmbiguous.
//
// Measured against production data on 2026-11-22 this resolves about one
// fixture in seven. That is the ceiling of what is available without a
// shared key, not a tuning problem: 212 of 312 fixtures had more than one
// candidate at five minutes and no score to separate them.
func linkRemainingByTime(
	lnwMatches []model.LnwMatch,
	thMatches []model.Match,
	byKey map[string]*model.LnwMatchLink,
	day string,
	now time.Time,
	cov *model.LnwCoverage,
) {
	// A thscore match already paired with a fixture must not be claimed
	// again. Only a row carrying both ids counts as paired: the name pass
	// also creates a row per unmatched thscore fixture, and treating those
	// as taken would empty the candidate pool entirely.
	taken := map[string]bool{}
	// Where a thscore fixture is still on its own, remember the key of that
	// row so a link made here can retire it — otherwise the same fixture is
	// counted once as linked and once as thscore-only.
	soloKey := map[string]string{}
	for key, l := range byKey {
		switch {
		case l.ThscoreID != "" && l.MatchID != "":
			taken[l.ThscoreID] = true
		case l.ThscoreID != "":
			soloKey[l.ThscoreID] = key
		}
	}

	for _, m := range lnwMatches {
		id := linkID(day, m.HomeName, m.AwayName)
		l, ok := byKey[id]
		if !ok || l.ThscoreID != "" {
			continue
		}

		var candidates []model.Match
		for _, th := range thMatches {
			if taken[th.ID] {
				continue
			}
			if absDuration(th.KickoffAt.Sub(m.KickoffAt)) <= scoreWindow {
				candidates = append(candidates, th)
			}
		}

		switch {
		case len(candidates) == 0:
			cov.UnlinkedNoCandidate++
			if !hasThai(m.HomeName) || !hasThai(m.AwayName) {
				cov.UnlinkedScript++
			}
			continue
		case len(candidates) == 1:
			attach(l, candidates[0].ID, model.LnwLinkTimeWindow, 0.5, now, taken)
			retire(byKey, soloKey, candidates[0].ID)
			continue
		}

		// Several candidates: the score separates them only when the match
		// has one, which before kick-off it does not.
		if m.HasScore {
			var hit []model.Match
			for _, th := range candidates {
				if th.HomeScore == m.HomeScore && th.AwayScore == m.AwayScore {
					hit = append(hit, th)
				}
			}
			if len(hit) == 1 {
				attach(l, hit[0].ID, model.LnwLinkTimeScore, 0.6, now, taken)
				retire(byKey, soloKey, hit[0].ID)
				cov.LinkedByScore++
				continue
			}
		}

		cov.UnlinkedAmbiguous++
		if !hasThai(m.HomeName) || !hasThai(m.AwayName) {
			cov.UnlinkedScript++
		}
	}
}

// attach records an inferred link and retires the thscore match from the
// candidate pool so no second fixture can claim it.
func attach(
	l *model.LnwMatchLink,
	thscoreID, method string,
	confidence float64,
	now time.Time,
	taken map[string]bool,
) {
	l.ThscoreID = thscoreID
	l.Method = method
	l.Confidence = confidence
	l.UpdatedAt = now
	taken[thscoreID] = true
}

// retire drops the standalone row a thscore fixture had before it was
// paired with a fixture from this vendor, so the day's totals still add up.
func retire(byKey map[string]*model.LnwMatchLink, soloKey map[string]string, thscoreID string) {
	if key, ok := soloKey[thscoreID]; ok {
		delete(byKey, key)
		delete(soloKey, thscoreID)
	}
}

// linkResultsByTime attaches result rows to fixture rows on kick-off alone,
// and only where the pairing is unambiguous.
func linkResultsByTime(
	results []model.LnwResult,
	matches []model.LnwMatch,
	byKey map[string]*model.LnwMatchLink,
	day string,
	now time.Time,
	cov *model.LnwCoverage,
) {
	taken := map[string]bool{}
	for _, r := range results {
		var candidates []model.LnwMatch
		for _, m := range matches {
			if taken[m.ID] {
				continue
			}
			if absDuration(m.KickoffAt.Sub(r.KickoffAt)) <= linkWindow {
				candidates = append(candidates, m)
			}
		}
		if len(candidates) != 1 {
			// Either nothing lines up or several do. Both are unresolvable
			// without a shared key, so the result is stored unlinked and
			// counted as such rather than attached to a guess.
			continue
		}
		m := candidates[0]
		id := linkID(day, m.HomeName, m.AwayName)
		l, ok := byKey[id]
		if !ok {
			continue
		}
		l.ResultID = r.ID
		if l.Method == "" || l.Method == model.LnwLinkExactName {
			// The pairing itself was found by name; the result attached to
			// it was not, so the weakest link in the chain is what the row
			// should advertise.
			l.Method = model.LnwLinkTimeWindow
			l.Confidence = 0.5
		}
		l.UpdatedAt = now
		taken[m.ID] = true
	}
}

func findMatchByID(matches []model.LnwMatch, id string) *model.LnwMatch {
	if id == "" {
		return nil
	}
	for i := range matches {
		if matches[i].ID == id {
			return &matches[i]
		}
	}
	return nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
