package lotto

import (
	"strconv"
	"time"

	"github.com/flip1688/livescore/internal/model"
)

// Market is one entry of /api/v1/lotteries. Latest is populated only by
// /api/v1/result/{slug} and by the history endpoint's envelope; the list
// endpoint omits it.
type Market struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	ShortName    string `json:"shortName"`
	Category     string `json:"category"`
	Tier         string `json:"tier"`
	Country      string `json:"country"`
	DrawSchedule string `json:"drawSchedule"`
	Latest       *Draw  `json:"latest"`
}

// History is the /history payload: the market, plus its draws under
// .results. The array is NOT at .data — decoding it as one silently yields
// an empty list.
type History struct {
	Market
	Count   int    `json:"count"`
	Results []Draw `json:"results"`
}

// Draw is one draw. DrawDate and PublishedAt are RFC3339 UTC — unlike
// thscore, this feed is not GMT+7, so conversion to Bangkok happens once,
// here, in ToModel.
type Draw struct {
	DrawID      int64     `json:"drawId"`
	DrawDate    time.Time `json:"drawDate"`
	Round       *int      `json:"round"`
	Status      string    `json:"status"`
	IsCanceled  bool      `json:"isCanceled"`
	PublishedAt time.Time `json:"publishedAt"`
	Prizes      []Prize   `json:"prizes"`
}

type Prize struct {
	Type   string `json:"type"`
	Label  string `json:"label"`
	Number string `json:"number"`
}

// WebhookEvent is the body upstream POSTs on result.published /
// result.updated. Only markets in the gov, lao, hanoi and stock categories
// are delivered.
type WebhookEvent struct {
	Event      string    `json:"event"`
	OccurredAt time.Time `json:"occurredAt"`
	Data       Market    `json:"data"`
}

// Webhook event names.
const (
	EventResultPublished = "result.published"
	EventResultUpdated   = "result.updated"
)

// Bangkok is the display timezone for draw days, matching the rest of the
// service. Loaded once; a failure falls back to a fixed +07:00 offset, which
// is correct for Thailand year-round (no DST).
var Bangkok = mustBangkok()

func mustBangkok() *time.Location {
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		return time.FixedZone("ICT", 7*60*60)
	}
	return loc
}

// ToModel normalizes one upstream draw for a market into the stored shape.
// It fills Complete; Superseded is decided later across a whole market's
// draws by model.MarkSuperseded.
func (d Draw) ToModel(m Market, now time.Time) model.LottoDraw {
	prizes := make([]model.LottoPrize, 0, len(d.Prizes))
	for _, p := range d.Prizes {
		prizes = append(prizes, model.LottoPrize{Type: p.Type, Label: p.Label, Number: p.Number})
	}
	drawAt := d.DrawDate.UTC()
	return model.LottoDraw{
		ID:          strconv.FormatInt(d.DrawID, 10),
		Slug:        m.Slug,
		Category:    m.Category,
		DrawAt:      drawAt,
		DrawDay:     drawAt.In(Bangkok).Format("2006-01-02"),
		Round:       d.Round,
		Status:      d.Status,
		IsCanceled:  d.IsCanceled,
		PublishedAt: d.PublishedAt.UTC(),
		Prizes:      prizes,
		Complete:    model.IsComplete(m.Slug, m.Category, d.IsCanceled, prizes),
		FetchedAt:   now,
		UpdatedAt:   now,
	}
}

// ToModel normalizes a market. Active and LatestDrawAt are left zero for the
// caller to stamp from the draws it actually stored.
func (m Market) ToModel(now time.Time) model.LottoMarket {
	return model.LottoMarket{
		Slug:         m.Slug,
		Name:         m.Name,
		ShortName:    m.ShortName,
		Category:     m.Category,
		Tier:         m.Tier,
		Country:      m.Country,
		DrawSchedule: m.DrawSchedule,
		UpdatedAt:    now,
	}
}
