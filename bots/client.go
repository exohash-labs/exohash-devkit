package bots

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client talks to the BFF via HTTP — same as a real player.
type Client struct {
	BaseURL string
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL}
}

type PlaceBetResponse struct {
	BetID  uint64 `json:"betId"`
	TxHash string `json:"txHash"`
	Error  string `json:"error"`
}

type BetActionResponse struct {
	TxHash string `json:"txHash"`
	Error  string `json:"error"`
}

func (c *Client) PlaceBet(addr string, bankrollID, calcID, stake uint64, params []byte) (uint64, error) {
	body := map[string]any{
		"address":      addr,
		"bankrollId":   bankrollID,
		"calculatorId": calcID,
		"stake":        fmt.Sprintf("%d", stake),
		"params":       params,
	}
	var resp PlaceBetResponse
	if err := c.post("/relay/place-bet", body, &resp); err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("%s", resp.Error)
	}
	return resp.BetID, nil
}

func (c *Client) BetAction(addr string, betID uint64, action []byte) error {
	body := map[string]any{
		"address": addr,
		"betId":   betID,
		"action":  action,
	}
	var resp BetActionResponse
	if err := c.post("/relay/bet-action", body, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func (c *Client) Faucet(addr string) error {
	body := map[string]any{"address": addr}
	var resp struct{ Error string `json:"error"` }
	if err := c.post("/faucet/request", body, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

type GameInfo struct {
	CalcID     uint64 `json:"calcId"`
	BankrollID uint64 `json:"bankrollId"`
	Engine     string `json:"engine"`
}

// FetchGames returns a map of engine name → GameInfo from the BFF.
func (c *Client) FetchGames() map[string]GameInfo {
	resp, err := http.Get(c.BaseURL + "/games")
	if err != nil {
		return map[string]GameInfo{}
	}
	defer resp.Body.Close()
	var games []GameInfo
	json.NewDecoder(resp.Body).Decode(&games)
	m := make(map[string]GameInfo)
	for _, g := range games {
		m[g.Engine] = g
	}
	return m
}

func (c *Client) post(path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := http.Post(c.BaseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}
