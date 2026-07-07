package thscore

import "github.com/flip1688/livescore/internal/model"

// Match status codes, shared by schedule/livescores/changes endpoints.
const (
	StatusNotStarted  = 0
	StatusFirstHalf   = 1
	StatusHalftime    = 2
	StatusSecondHalf  = 3
	StatusExtraTime   = 4
	StatusPenalty     = 5
	StatusFinished    = -1
	StatusCancelled   = -10
	StatusTBD         = -11
	StatusTerminated  = -12
	StatusInterrupted = -13
	StatusPostponed   = -14
)

// MapStatus normalizes a thscore status code into our domain status.
func MapStatus(code int) model.MatchStatus {
	switch code {
	case StatusNotStarted, StatusTBD:
		return model.MatchScheduled
	case StatusFirstHalf, StatusSecondHalf, StatusExtraTime, StatusPenalty:
		return model.MatchLive
	case StatusHalftime:
		return model.MatchHalftime
	case StatusFinished:
		return model.MatchFinished
	case StatusPostponed, StatusInterrupted:
		return model.MatchPostponed
	case StatusCancelled, StatusTerminated:
		return model.MatchCanceled
	default:
		return model.MatchScheduled
	}
}

// Event type codes from /football_th/events.aspx.
const (
	EventGoal          = 1
	EventRedCard       = 2
	EventYellowCard    = 3
	EventPenaltyScored = 7
	EventOwnGoal       = 8
	EventSecondYellow  = 9
	EventSubstitution  = 11
	EventPenaltyMissed = 13
	EventVAR           = 14
)
