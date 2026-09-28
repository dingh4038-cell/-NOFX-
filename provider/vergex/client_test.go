package vergex

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestParseDirectionChangeLeaderboard(t *testing.T) {
	body := []byte(`{
		"band":15,"universeSize":30,"rankBy":"directionScore","asOf":1786781428957,
		"items":[
			{"market":{"marketType":"hip3_perp","symbol":"xyz:NVDA"},"symbol":"xyz:NVDA","bias":"bullish","directionScore":4,"bullishCount":4,"bearishCount":0,"neutralCount":1,"markPrice":224.5,"rank":1,"oiRank":14},
			{"market":{"marketType":"core_perp","symbol":"BTC"},"symbol":"BTC","bias":"bearish","directionScore":-4,"bullishCount":0,"bearishCount":4,"neutralCount":1,"markPrice":63047,"rank":2,"oiRank":1}
		]
	}`)

	board, err := ParseDirectionChangeLeaderboard(body)
	if err != nil {
		t.Fatal(err)
	}
	if board.Band != 15 || board.UniverseSize != 30 || board.RankBy != "directionScore" || len(board.Items) != 2 {
		t.Fatalf("unexpected board metadata/items: %+v", board)
	}
	nvda := board.Items[0]
	if nvda.Symbol != "NVDA" || nvda.APISymbol != "xyz:NVDA" || nvda.MarketType != "hip3_perp" || nvda.Score != 4 || nvda.BullishCount != 4 || nvda.MarkPrice != 224.5 || nvda.OIRank != 14 {
		t.Fatalf("unexpected parsed NVDA item: %+v", nvda)
	}
	filtered := FilterDirectionChangeItems(board.Items, "all", 30)
	if len(filtered) != 2 || filtered[0].Category != "stock" || filtered[1].Category != "crypto" {
		t.Fatalf("unexpected filtered items: %+v", filtered)
	}
}

func TestDirectionChangeRequestsUseExactPathsAndParams(t *testing.T) {
	type seenRequest struct {
		path  string
		query url.Values
	}
	var mu sync.Mutex
	var seen []seenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, seenRequest{path: r.URL.Path, query: r.URL.Query()})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case DirectionLeaderboardPath:
			fmt.Fprint(w, `{"band":15,"universeSize":0,"rankBy":"directionScore","asOf":1,"items":[]}`)
		case DirectionCurrentPath:
			fmt.Fprint(w, `{"symbol":"BTC","direction":"bearish"}`)
		case DirectionHistoryPath:
			fmt.Fprint(w, `{"items":[],"pagination":{"current_page":2,"page_size":100,"total_pages":0,"total_items":0}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	if _, err := client.GetDirectionChangeLeaderboard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetDirectionChangeCurrent(context.Background(), " BTC "); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetDirectionChangeHistory(context.Background(), "BTC", "reversal", 2, 500); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 3 {
		t.Fatalf("requests=%d, want 3", len(seen))
	}
	if seen[0].path != DirectionLeaderboardPath || len(seen[0].query) != 0 {
		t.Fatalf("leaderboard request=%+v", seen[0])
	}
	if seen[1].path != DirectionCurrentPath || seen[1].query.Get("symbol") != "BTC" || len(seen[1].query) != 1 {
		t.Fatalf("current request=%+v", seen[1])
	}
	q := seen[2].query
	if seen[2].path != DirectionHistoryPath || q.Get("symbol") != "BTC" || q.Get("type") != "reversal" || q.Get("page") != "2" || q.Get("page_size") != "100" || len(q) != 4 {
		t.Fatalf("history request=%+v", seen[2])
	}
}

func TestDirectionChangeValidationAndHistoryDefaults(t *testing.T) {
	client := testClient(t, "http://127.0.0.1:1")
	if _, err := client.GetDirectionChangeCurrent(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "symbol is required") {
		t.Fatalf("current validation err=%v", err)
	}
	if _, err := client.GetDirectionChangeHistory(context.Background(), "BTC", "bad", 1, 20); err == nil || !strings.Contains(err.Error(), "type must be") {
		t.Fatalf("history type err=%v", err)
	}
}

func TestDirectionChangeLiveIntegration(t *testing.T) {
	if os.Getenv("VERGEX_INTEGRATION") != "1" {
		t.Skip("set VERGEX_INTEGRATION=1 to run paid claw402 integration")
	}
	client, err := NewClient("", os.Getenv("CLAW402_WALLET_KEY"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	board, err := client.GetDirectionChangeLeaderboard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if board.Band <= 0 || board.UniverseSize <= 0 || len(board.Items) == 0 || len(board.Items) > MaxDirectionChangeItems {
		t.Fatalf("invalid live leaderboard metadata: band=%d universe=%d items=%d", board.Band, board.UniverseSize, len(board.Items))
	}
	symbol := board.Items[0].APISymbol
	currentRaw, err := client.GetDirectionChangeCurrent(ctx, symbol)
	if err != nil {
		t.Fatalf("current(%s): %v", symbol, err)
	}
	var current struct {
		Symbol    string `json:"symbol"`
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(currentRaw, &current); err != nil {
		t.Fatal(err)
	}
	if current.Symbol == "" || current.Direction == "" {
		t.Fatalf("invalid current response: %s", currentRaw)
	}
	historyRaw, err := client.GetDirectionChangeHistory(ctx, symbol, "all", 1, 2)
	if err != nil {
		t.Fatalf("history(%s): %v", symbol, err)
	}
	var history struct {
		Items      []json.RawMessage `json:"items"`
		Pagination struct {
			CurrentPage int `json:"current_page"`
			PageSize    int `json:"page_size"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(historyRaw, &history); err != nil {
		t.Fatal(err)
	}
	if history.Pagination.CurrentPage != 1 || history.Pagination.PageSize != 2 {
		t.Fatalf("invalid history pagination: %s", historyRaw)
	}
}

func TestFormatAnalysisForAIUsesDirectionChangeData(t *testing.T) {
	text := FormatAnalysisForAI(&MarketAnalysis{
		Symbol: "BTC", QuerySymbol: "BTC", MarketType: "core_perp",
		Ranking:          &DirectionChangeItem{Rank: 25, Bias: "bearish", Score: -4, BearishCount: 4, NeutralCount: 1, OIRank: 1, MarkPrice: 63047, Category: "crypto"},
		DirectionCurrent: json.RawMessage(`{"symbol":"BTC","direction":"bearish"}`),
		DirectionHistory: json.RawMessage(`{"items":[{"prev_bias":"bullish","new_bias":"bearish"}]}`),
	})
	for _, want := range []string{"Direction leaderboard", "direction_score=-4", "Current Bull/Bear Direction", "Bull/Bear Direction History", `"new_bias":"bearish"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Signal Lab") {
		t.Fatalf("old Signal Lab label remains:\n%s", text)
	}
}

func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return &Client{baseURL: baseURL, privateKey: (*ecdsa.PrivateKey)(key), httpClient: http.DefaultClient}
}
