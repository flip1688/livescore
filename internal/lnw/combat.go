package lnw

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The combat feeds: Thai boxing programmes (/api/public/boxing), the
// round-by-round muay feed (/api/public/muay-pakyok) and the bull- and
// cock-fighting feed (/api/public/wuachon).
//
// These are the feeds thscore has no equivalent of, so they are the part of
// this vendor worth buying rather than the football it also carries.

// SportBoxing and friends name a combat feed in LnwSyncer and in storage.
// They are our names, not upstream's: upstream serves pakyok and wuachon
// through differently shaped endpoints and mixes two sports inside the
// wuachon one (see CombatBout.Type).
const (
	SportBoxing  = "boxing"
	SportPakyok  = "muay_pakyok"
	SportWuachon = "wuachon"
)

// BoxingResponse is /api/public/boxing.
//
// It lists the programmes ("ศึก") and nothing else: every programme on
// 2026-11-22 carried totalEvents 0, and the endpoint ignores date, day and
// programId parameters — the same scrapedAt comes back whatever is asked.
// So this is a scraped snapshot of which programmes exist, not a card.
type BoxingResponse struct {
	OK    bool   `json:"ok"`
	Tier  string `json:"tier"`
	Count int    `json:"count"`
	Data  struct {
		Programs  []BoxingProgram `json:"programs"`
		Total     int             `json:"total"`
		ScrapedAt string          `json:"scrapedAt"`
	} `json:"data"`
}

type BoxingProgram struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Logo   string `json:"logo"`
	Splash string `json:"splash"`

	// Events is deliberately left raw. It has only ever been observed
	// empty, so typing it would mean inventing a shape and then storing
	// data through a guess. Keeping the bytes means the first real card
	// is captured intact and can be typed from evidence afterwards —
	// SyncBoxing logs loudly when a non-empty one arrives.
	Events      json.RawMessage `json:"events"`
	TotalEvents int             `json:"totalEvents"`
}

// HasEvents reports whether a programme carried a card. TotalEvents is
// trusted over len(Events) only when the raw array is absent, because the
// two have never been seen to disagree and the count is the cheaper check.
func (p BoxingProgram) HasEvents() bool {
	if p.TotalEvents > 0 {
		return true
	}
	trimmed := strings.TrimSpace(string(p.Events))
	return trimmed != "" && trimmed != "[]" && trimmed != "null"
}

// CombatResponse is /api/public/wuachon and /api/public/muay-pakyok.
//
// Stadiums is not dependable: one fetch carried 152 entries and the next,
// fifteen minutes later, carried none. Callers must treat it as a bonus
// dictionary, never as the source of a bout's venue — the bout carries its
// own Stadium string.
type CombatResponse struct {
	OK        bool            `json:"ok"`
	Tier      string          `json:"tier"`
	Total     int             `json:"total"`
	Live      int             `json:"live"`
	Source    string          `json:"source"`
	TS        int64           `json:"_ts"` // epoch milliseconds
	Data      []CombatBout    `json:"data"`
	Stadiums  []CombatStadium `json:"stadiums"`
	ScrapedAt string          `json:"scrapedAt"`
}

type CombatStadium struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDisplay bool   `json:"is_display"`
	Type      string `json:"type"`
}

// CombatBout is one fight.
type CombatBout struct {
	ID int64 `json:"id"`
	// RoundName packs three facts into one string:
	// "22/11/69 สนามชนไก่ดาวอังคาร คู่ที่ 33" — Buddhist-era short date,
	// venue, and the bout's position on the card. BoutNumber reads the last.
	RoundName string `json:"roundName"`

	// Type is "cock" or "wuachon": the wuachon endpoint serves both sports.
	Type      string `json:"type"`
	TypeLabel string `json:"typeLabel"`

	RedCorner  CombatCorner `json:"redCorner"`
	BlueCorner CombatCorner `json:"blueCorner"`

	Stadium   string `json:"stadium"`
	StadiumID int64  `json:"stadiumId"`

	// DateTime is RFC3339 with an explicit +07:00 offset.
	DateTime string `json:"dateTime"`

	Status      string `json:"status"`      // "open" | "ended"
	StatusLabel string `json:"statusLabel"` // "เปิดรับ" | "จบแล้ว"
	Result      string `json:"result"`      // "red" | "blue" | "tie" | "green" | ""
	ResultLabel string `json:"resultLabel"`

	Stakes      int64  `json:"stakes"`
	StakesLabel string `json:"stakesLabel"`

	Stats CombatStats `json:"stats"`

	// IsLive contradicts Status: 200 of 212 bouts on 2026-11-22 were
	// status "ended" and some of those still carried isLive true. Status is
	// the field to believe; this one is kept only so the disagreement stays
	// visible in storage.
	IsLive     bool `json:"isLive"`
	IsPrefight bool `json:"isPrefight"`

	// Stream is parsed only so its presence can be recorded. Its url
	// carries an stk= token that base64-decodes to the account's own API
	// key, which is why neither this field nor playerUrl/iframeUrl may ever
	// reach storage or a response. HasStream is the only thing taken from it.
	Stream json.RawMessage `json:"stream"`
}

// HasStream reports whether upstream offered a stream for this bout.
func (b CombatBout) HasStream() bool {
	t := strings.TrimSpace(string(b.Stream))
	return t != "" && t != "null" && t != "{}"
}

type CombatCorner struct {
	Name string `json:"name"`
}

type CombatStats struct {
	RedWinPercent  float64  `json:"redWinPercent"`
	BlueWinPercent float64  `json:"blueWinPercent"`
	RedFights      int      `json:"redFights"`
	BlueFights     int      `json:"blueFights"`
	RedWins        int      `json:"redWins"`
	BlueWins       int      `json:"blueWins"`
	RedWeight      float64  `json:"redWeight"`
	BlueWeight     float64  `json:"blueWeight"`
	WeightUnit     string   `json:"weightUnit"`
	RedForm        []string `json:"redForm"`
	BlueForm       []string `json:"blueForm"`
	Opinion        string   `json:"opinion"`
}

var boutNumberRe = regexp.MustCompile(`คู่ที่\s*(\d+)`)

// BoutNumber reads the bout's position on the card out of RoundName.
// Zero means the string did not carry one, which is not an error: the
// design calls for "คู่ที่ N" only where upstream states it.
func BoutNumber(roundName string) int {
	m := boutNumberRe.FindStringSubmatch(roundName)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// ParseCombatTime reads CombatBout.DateTime. Unlike the football result
// feed this one states its offset, so it is taken at face value.
func ParseCombatTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// CombatResult maps upstream's winner field onto a value worth storing.
//
// "green" is passed through as unknown rather than renamed: it appeared on
// 10 of 212 bouts with resultLabel also reading "green", i.e. upstream is
// leaking an unmapped internal value, and inventing a Thai meaning for it
// would bury that.
func CombatResult(result string) (normalized string, known bool) {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "red":
		return "red", true
	case "blue":
		return "blue", true
	case "tie":
		return "tie", true
	case "":
		return "", true // not decided yet; status says why
	default:
		return "", false
	}
}
