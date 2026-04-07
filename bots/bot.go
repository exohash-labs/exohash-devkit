package bots

import "encoding/json"

// Action is what a bot wants to do in response to an event.
type Action struct {
	Type   ActionType
	Stake  uint64
	BetID  uint64 // for bet_action
	Params []byte // for place_bet
	Action []byte // for bet_action
}

type ActionType int

const (
	DoNothing ActionType = iota
	DoPlaceBet
	DoBetAction
)

// Bot is the interface all game bots implement.
type Bot interface {
	Address() string
	CalcID() uint64
	OnEvent(topic string, data json.RawMessage) Action
	SetBetID(betID uint64)
}

func None() Action                                { return Action{Type: DoNothing} }
func PlaceBet(stake uint64, params []byte) Action { return Action{Type: DoPlaceBet, Stake: stake, Params: params} }
func BetAction(betID uint64, action []byte) Action {
	return Action{Type: DoBetAction, BetID: betID, Action: action}
}
