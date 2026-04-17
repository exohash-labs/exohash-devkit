package chainsim

import (
	"os"
	"testing"
)

func TestInjectGasMetering_Dice(t *testing.T) {
	raw, err := os.ReadFile("../games/dice/dice.wasm")
	if err != nil {
		t.Skipf("dice.wasm not found: %v", err)
	}

	instrumented, err := InjectGasMetering(raw)
	if err != nil {
		t.Fatalf("InjectGasMetering: %v", err)
	}

	t.Logf("original=%d instrumented=%d overhead=+%d bytes",
		len(raw), len(instrumented), len(instrumented)-len(raw))

	if len(instrumented) <= len(raw) {
		t.Error("instrumented should be larger than original")
	}

	// Verify it's valid WASM (magic + version).
	if string(instrumented[:4]) != "\x00asm" {
		t.Error("invalid WASM magic")
	}
}

func TestInjectGasMetering_AllGames(t *testing.T) {
	games := []string{"dice", "crash", "mines"}
	for _, game := range games {
		t.Run(game, func(t *testing.T) {
			raw, err := os.ReadFile("../games/" + game + "/" + game + ".wasm")
			if err != nil {
				t.Skipf("%s.wasm not found: %v", game, err)
			}

			instrumented, err := InjectGasMetering(raw)
			if err != nil {
				t.Fatalf("InjectGasMetering(%s): %v", game, err)
			}

			t.Logf("%s: %d → %d bytes (+%d)",
				game, len(raw), len(instrumented), len(instrumented)-len(raw))
		})
	}
}
