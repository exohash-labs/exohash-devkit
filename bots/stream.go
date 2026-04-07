package bots

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// StreamEvent matches the mock-bff SSE protocol.
type StreamEvent struct {
	Height      uint64       `json:"height"`
	Time        string       `json:"time,omitempty"`
	CalcEvents  []CalcEvent  `json:"calcEvents,omitempty"`
	BetsSettled []BetSettled `json:"betsSettled,omitempty"`
	Connected   bool         `json:"connected,omitempty"`
	Replay      *bool        `json:"replay,omitempty"`
	Heartbeat   bool         `json:"heartbeat,omitempty"`
}

type CalcEvent struct {
	CalculatorID uint64 `json:"calculatorId"`
	Topic        string `json:"topic"`
	Data         string `json:"data"`
}

type BetSettled struct {
	BetID      uint64 `json:"betId"`
	GameID     uint64 `json:"gameId"`
	PayoutKind int    `json:"payoutKind"`
}

// Stream connects to mock-bff SSE and delivers events on a channel.
type Stream struct {
	baseURL string
	events  chan StreamEvent
	done    chan struct{}
}

func NewStream(baseURL string) *Stream {
	return &Stream{
		baseURL: baseURL,
		events:  make(chan StreamEvent, 128),
		done:    make(chan struct{}),
	}
}

// Events returns the channel of live events.
func (s *Stream) Events() <-chan StreamEvent {
	return s.events
}

// Connect starts the SSE connection in a goroutine with auto-reconnect.
func (s *Stream) Connect() {
	go s.loop()
}

// Close stops the stream.
func (s *Stream) Close() {
	close(s.done)
}

func (s *Stream) loop() {
	for {
		select {
		case <-s.done:
			return
		default:
		}

		if err := s.readSSE(); err != nil {
			select {
			case <-s.done:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (s *Stream) readSSE() error {
	resp, err := http.Get(s.baseURL + "/stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("SSE status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for large SSE messages.
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)

	replaying := false

	for scanner.Scan() {
		select {
		case <-s.done:
			return nil
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]

		var ev StreamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}

		// Skip replay phase — bots only care about live events.
		if ev.Connected && ev.Replay != nil && *ev.Replay {
			replaying = true
			continue
		}
		if ev.Replay != nil && !*ev.Replay {
			replaying = false
			continue
		}
		if replaying || ev.Heartbeat {
			continue
		}

		select {
		case s.events <- ev:
		default:
			// drop if consumer is slow
		}
	}

	return scanner.Err()
}
