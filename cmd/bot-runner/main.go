package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/exohash-labs/exohash-devkit/bots"
)

func main() {
	cfgPath := "bots.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := bots.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	client := bots.NewClient(cfg.BffURL)
	runner := bots.NewRunner(client)

	// Create crash bots.
	for _, c := range cfg.Crash {
		addr := randomAddr(c.Name)
		bot := bots.NewCrashBot(bots.CrashBotConfig{
			Address: addr,
			CalcID:  2,
			Stake:   c.Stake,
			Cashout: c.Cashout,
		})
		runner.AddBot(bot)
		log.Printf("Crash bot: %s (%s, target=%.1fx)", c.Name, addr[:16]+"...", float64(c.Cashout)/10000)
	}

	// Create dice bots.
	for i, d := range cfg.Dice {
		addr := randomAddr(d.Name)
		bot := bots.NewDiceBot(bots.DiceBotConfig{
			Address:  addr,
			CalcID:   1,
			MinStake: d.Stake,
			MaxStake: d.Stake,
			ChanceBP: d.ChanceBP,
			Every:    d.Every,
			Seed:     int64(i * 1000),
		})
		runner.AddBot(bot)
		log.Printf("Dice bot: %s (chance=%d%%, every=%d)", d.Name, d.ChanceBP/100, d.Every)
	}

	// Create mines bots.
	for i, m := range cfg.Mines {
		addr := randomAddr(m.Name)
		bot := bots.NewMinesBot(bots.MinesBotConfig{
			Address:    addr,
			CalcID:     3,
			MinStake:   m.Stake,
			MaxStake:   m.Stake,
			MinesCount: m.Mines,
			MaxReveals: m.Reveals,
			Every:      m.Every,
			Seed:       int64(i * 2000),
		})
		runner.AddBot(bot)
		log.Printf("Mines bot: %s (mines=%d, reveals=%d, every=%d)", m.Name, m.Mines, m.Reveals, m.Every)
	}

	// Fund all bots.
	runner.FundBots()

	// Connect SSE stream.
	stream := bots.NewStream(cfg.BffURL)
	stream.Connect()
	defer stream.Close()

	log.Printf("Bot runner connected to %s — %d bots active", cfg.BffURL, len(cfg.Crash)+len(cfg.Dice)+len(cfg.Mines))

	// Event loop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	for {
		select {
		case <-sigCh:
			log.Println("Shutting down bots...")
			return
		case ev := <-stream.Events():
			runner.ProcessEvent(ev)
		}
	}
}

func randomAddr(name string) string {
	b := make([]byte, 20)
	rand.Read(b)
	return fmt.Sprintf("exo1%s", hex.EncodeToString(b))
}
