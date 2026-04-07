package chainsim

import "fmt"

// ChainEvent mirrors Cosmos SDK events emitted by the house module.
type ChainEvent struct {
	Type  string            // e.g. "bankroll_created", "bet_created", "bet_settled"
	Attrs map[string]string // key-value attributes
}

func (c *Chain) emit(typ string, pairs ...string) {
	ev := ChainEvent{Type: typ, Attrs: make(map[string]string)}
	for i := 0; i+1 < len(pairs); i += 2 {
		ev.Attrs[pairs[i]] = pairs[i+1]
	}
	c.events = append(c.events, ev)
}

// DrainEvents returns all events since last drain and clears the buffer.
func (c *Chain) DrainEvents() []ChainEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ChainEvent, len(c.events))
	copy(out, c.events)
	c.events = c.events[:0]
	return out
}

// u64 is a helper to format uint64 for event attrs.
func u64(v uint64) string { return fmt.Sprintf("%d", v) }
