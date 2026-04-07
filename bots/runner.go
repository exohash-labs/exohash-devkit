package bots

import (
	"encoding/json"
	"log"
)

// Runner drives bots by feeding SSE events and executing their actions via HTTP.
type Runner struct {
	client *Client
	bots   []Bot
}

func NewRunner(client *Client) *Runner {
	return &Runner{client: client}
}

func (r *Runner) AddBot(bot Bot) {
	r.bots = append(r.bots, bot)
}

// FundBots gives each bot faucet tokens.
func (r *Runner) FundBots() {
	for _, bot := range r.bots {
		if err := r.client.Faucet(bot.Address()); err != nil {
			log.Printf("faucet failed for %s: %v", bot.Address(), err)
		} else {
			log.Printf("Funded bot: %s", bot.Address())
		}
	}
}

// ProcessEvent feeds an SSE event to all bots and executes their actions.
func (r *Runner) ProcessEvent(ev StreamEvent) {
	// Feed game-specific calc events.
	for _, ce := range ev.CalcEvents {
		for _, bot := range r.bots {
			if ce.CalculatorID != bot.CalcID() {
				continue
			}
			action := bot.OnEvent(ce.Topic, json.RawMessage(ce.Data))
			r.execute(bot, action)
		}
	}

	// Block tick — drives timer-based bots.
	for _, bot := range r.bots {
		action := bot.OnEvent("block", nil)
		r.execute(bot, action)
	}
}

func (r *Runner) execute(bot Bot, action Action) {
	switch action.Type {
	case DoPlaceBet:
		betID, err := r.client.PlaceBet(bot.Address(), 1, bot.CalcID(), action.Stake, action.Params)
		if err != nil {
			return
		}
		log.Printf("BOT %s: placed betID=%d", bot.Address(), betID)
		bot.SetBetID(betID)

	case DoBetAction:
		if action.BetID == 0 {
			return
		}
		if err := r.client.BetAction(bot.Address(), action.BetID, action.Action); err != nil {
			log.Printf("BOT %s: action failed for betID=%d: %v", bot.Address(), action.BetID, err)
		}
	}
}
