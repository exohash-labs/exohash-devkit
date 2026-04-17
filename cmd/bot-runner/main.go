package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

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

	// Load pre-created bot addresses from environment.
	// Real chain: addresses must have keys on the chain keyring with authz grants to the relay.
	// bffsim: leave BOT_ADDRS unset — fresh bech32 addresses are generated (faucet + authz mocks require nothing).
	var botAddrs []string
	if env := os.Getenv("BOT_ADDRS"); env != "" {
		botAddrs = strings.Split(env, ",")
	} else {
		need := len(cfg.Crash) + len(cfg.Dice) + len(cfg.Mines)
		for i := 0; i < need; i++ {
			botAddrs = append(botAddrs, randomAddr(fmt.Sprintf("bot-%d", i)))
		}
		log.Printf("BOT_ADDRS unset — generated %d fresh addresses (bffsim mode)", need)
	}

	client := bots.NewClient(cfg.BffURL)
	runner := bots.NewRunner(client)

	// Fetch game→bankroll mapping from BFF.
	gameMap := client.FetchGames()
	diceGame := gameMap["dice"]
	crashGame := gameMap["crash"]
	minesGame := gameMap["mines"]
	log.Printf("Games: dice(calc=%d,br=%d) crash(calc=%d,br=%d) mines(calc=%d,br=%d)",
		diceGame.CalcID, diceGame.BankrollID, crashGame.CalcID, crashGame.BankrollID, minesGame.CalcID, minesGame.BankrollID)

	addrIdx := 0
	nextAddr := func() string {
		a := botAddrs[addrIdx]
		addrIdx++
		return a
	}

	// Create crash bots.
	for _, c := range cfg.Crash {
		addr := nextAddr()
		bot := bots.NewCrashBot(bots.CrashBotConfig{
			Address:    addr,
			CalcID:     crashGame.CalcID,
			BankrollID: crashGame.BankrollID,
			Stake:      c.Stake,
			Cashout:    c.Cashout,
		})
		runner.AddBot(bot)
		log.Printf("Crash bot: %s (%s, target=%.1fx)", c.Name, addr[:16]+"...", float64(c.Cashout)/10000)
	}

	// Create dice bots.
	for i, d := range cfg.Dice {
		addr := nextAddr()
		bot := bots.NewDiceBot(bots.DiceBotConfig{
			Address:    addr,
			CalcID:     diceGame.CalcID,
			BankrollID: diceGame.BankrollID,
			MinStake:   d.Stake,
			MaxStake:   d.Stake,
			ChanceBP:   d.ChanceBP,
			Every:      d.Every,
			Seed:       int64(i * 1000),
		})
		runner.AddBot(bot)
		log.Printf("Dice bot: %s (chance=%d%%, every=%d)", d.Name, d.ChanceBP/100, d.Every)
	}

	// Create mines bots.
	for i, m := range cfg.Mines {
		addr := nextAddr()
		bot := bots.NewMinesBot(bots.MinesBotConfig{
			Address:    addr,
			CalcID:     minesGame.CalcID,
			BankrollID: minesGame.BankrollID,
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
	addr, err := bech32Encode("exo", b)
	if err != nil {
		log.Fatalf("bech32: %v", err)
	}
	return addr
}

// bech32 encoding (BIP-173)
const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Encode(hrp string, data []byte) (string, error) {
	conv, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	combined := append(conv, []byte{0, 0, 0, 0, 0, 0}...)
	polymod := bech32Polymod(expandHRP(hrp, combined)) ^ 1
	for i := 0; i < 6; i++ {
		combined[len(conv)+i] = byte((polymod >> uint(5*(5-i))) & 31)
	}
	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1')
	for _, b := range combined {
		sb.WriteByte(bech32Charset[b])
	}
	return sb.String(), nil
}

func expandHRP(hrp string, data []byte) []byte {
	ret := make([]byte, len(hrp)*2+1+len(data))
	for i, c := range hrp {
		ret[i] = byte(c >> 5)
		ret[i+len(hrp)+1] = byte(c & 31)
	}
	copy(ret[len(hrp)*2+1:], data)
	return ret
}

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint32(0)
	bits := uint(0)
	var ret []byte
	maxv := uint32((1 << toBits) - 1)
	for _, b := range data {
		acc = (acc << fromBits) | uint32(b)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	return ret, nil
}
