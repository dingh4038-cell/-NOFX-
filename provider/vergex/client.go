package vergex

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"nofx/mcp"
	"nofx/mcp/payment"
	"nofx/provider/hyperliquid"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

const (
	DefaultBaseURL             = "https://claw402.ai"
	DefaultChain               = "mainnet"
	DefaultMarketType          = "hip3_perp"
	MaxDirectionChangeItems    = 30
	DirectionLeaderboardPath   = "/api/v1/vergex/direction-change/leaderboard"
	DirectionCurrentPath       = "/api/v1/vergex/direction-change/current"
	DirectionHistoryPath       = "/api/v1/vergex/direction-change/history"
	CostLiquidationHeatmapPath = "/api/v1/vergex/cost-liquidation-heatmap"
	FlowMarketsPath            = "/api/v1/vergex/flow-markets"
)

type Client struct {
	baseURL    string
	privateKey *ecdsa.PrivateKey
	httpClient *http.Client
	logger     mcp.Logger
}

type Query struct {
	MarketType string
	Symbol     string
	Chain      string
	LiqBand    string
	Category   string
}

type DirectionChangeLeaderboardData struct {
	Band         int                   `json:"band"`
	UniverseSize int                   `json:"universeSize"`
	RankBy       string                `json:"rankBy"`
	AsOf         int64                 `json:"asOf"`
	Raw          json.RawMessage       `json:"raw,omitempty"`
	Items        []DirectionChangeItem `json:"items"`
}

type DirectionChangeItem struct {
	Rank         int             `json:"rank,omitempty"`
	Symbol       string          `json:"symbol"`
	APISymbol    string          `json:"api_symbol,omitempty"`
	MarketType   string          `json:"market_type,omitempty"`
	Bias         string          `json:"bias,omitempty"`
	Score        float64         `json:"score,omitempty"`
	BullishCount int             `json:"bullish_count,omitempty"`
	BearishCount int             `json:"bearish_count,omitempty"`
	NeutralCount int             `json:"neutral_count,omitempty"`
	OIRank       int             `json:"oi_rank,omitempty"`
	MarkPrice    float64         `json:"mark_price,omitempty"`
	Category     string          `json:"category,omitempty"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

type MarketAnalysis struct {
	Symbol                string               `json:"symbol"`
	QuerySymbol           string               `json:"query_symbol"`
	MarketType            string               `json:"market_type"`
	Ranking               *DirectionChangeItem `json:"ranking,omitempty"`
	DirectionCurrent      json.RawMessage      `json:"direction_current,omitempty"`
	DirectionCurrentError string               `json:"direction_current_error,omitempty"`
	DirectionHistory      json.RawMessage      `json:"direction_history,omitempty"`
	DirectionHistoryError string               `json:"direction_history_error,omitempty"`
	Heatmap               json.RawMessage      `json:"heatmap,omitempty"`
	HeatmapError          string               `json:"heatmap_error,omitempty"`
}

func NewClient(baseURL, privateKeyHex string, logger mcp.Logger) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if privateKeyHex == "" {
		privateKeyHex = os.Getenv("CLAW402_WALLET_KEY")
	}
	if privateKeyHex == "" {
		return nil, fmt.Errorf("claw402 wallet private key not set")
	}
	if logger == nil {
		logger = mcp.NewNoopLogger()
	}

	hexKey := strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	pk, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid claw402 private key: %w", err)
	}

	return &Client{
		baseURL:    baseURL,
		privateKey: pk,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}, nil
}

func (c *Client) GetDirectionChangeLeaderboard(ctx context.Context) (*DirectionChangeLeaderboardData, error) {
	body, err := c.doGET(ctx, DirectionLeaderboardPath, nil)
	if err != nil {
		return nil, err
	}
	return ParseDirectionChangeLeaderboard(body)
}

func (c *Client) GetDirectionChangeCurrent(ctx context.Context, symbol string) (json.RawMessage, error) {
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	params := url.Values{"symbol": {strings.TrimSpace(symbol)}}
	return c.doGET(ctx, DirectionCurrentPath, params)
}

func (c *Client) GetDirectionChangeHistory(ctx context.Context, symbol, eventType string, page, pageSize int) (json.RawMessage, error) {
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		eventType = "all"
	}
	if eventType != "all" && eventType != "reversal" && eventType != "non_reversal" {
		return nil, fmt.Errorf("type must be all, reversal, or non_reversal")
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	params := url.Values{
		"symbol":    {strings.TrimSpace(symbol)},
		"type":      {eventType},
		"page":      {fmt.Sprintf("%d", page)},
		"page_size": {fmt.Sprintf("%d", pageSize)},
	}
	return c.doGET(ctx, DirectionHistoryPath, params)
}

func (c *Client) GetCostLiquidationHeatmap(ctx context.Context, q Query) (json.RawMessage, error) {
	if strings.TrimSpace(q.MarketType) == "" || strings.TrimSpace(q.Symbol) == "" {
		return nil, fmt.Errorf("marketType and symbol are required")
	}
	params := url.Values{}
	addQueryDefaults(params, q, true)
	return c.doGET(ctx, CostLiquidationHeatmapPath, params)
}

// GetFlowMarkets fetches the Vergex net-flow market ranking via the paid
// claw402 x402 endpoint. Params mirror the public API: chain (e.g. "mainnet"),
// window (e.g. "1h"), and limit. The raw JSON is returned for the caller to
// pass through — the response shape is owned by Vergex.
func (c *Client) GetFlowMarkets(ctx context.Context, chain, window string, limit int) (json.RawMessage, error) {
	params := url.Values{}
	if v := strings.TrimSpace(chain); v != "" {
		params.Set("chain", v)
	}
	if v := strings.TrimSpace(window); v != "" {
		params.Set("window", v)
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	return c.doGET(ctx, FlowMarketsPath, params)
}

func addQueryDefaults(params url.Values, q Query, includeMarket bool) {
	if includeMarket {
		if q.MarketType != "" {
			params.Set("marketType", q.MarketType)
		}
		if q.Symbol != "" {
			params.Set("symbol", MarketSymbol(q.MarketType, q.Symbol))
		}
	}
	if q.Chain != "" {
		params.Set("chain", QueryChain(q.Chain))
	}
	if q.LiqBand != "" {
		params.Set("liqBand", q.LiqBand)
	}
}

func (c *Client) doGET(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("vergex client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fullURL := c.baseURL + path
	if encoded := params.Encode(); encoded != "" {
		fullURL += "?" + encoded
	}

	buildReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Client-ID", "nofx")
		return req, nil
	}

	body, err := payment.DoX402Request(
		ctx,
		c.httpClient,
		buildReq,
		payment.MakeClaw402SignFunc(c.privateKey),
		"claw402-vergex",
		c.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("vergex request failed (%s): %w", path, err)
	}
	return body, nil
}

func ParseDirectionChangeLeaderboard(body []byte) (*DirectionChangeLeaderboardData, error) {
	raw := json.RawMessage(append([]byte(nil), body...))
	var meta struct {
		Band         int    `json:"band"`
		UniverseSize int    `json:"universeSize"`
		RankBy       string `json:"rankBy"`
		AsOf         int64  `json:"asOf"`
	}
	_ = json.Unmarshal(body, &meta)
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse vergex direction-change leaderboard response: %w", err)
	}

	rows := findObjectArray(decoded)
	items := make([]DirectionChangeItem, 0, len(rows))
	for idx, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			continue
		}
		item, ok := parseRankItem(obj, idx+1)
		if ok {
			items = append(items, item)
		}
	}

	return &DirectionChangeLeaderboardData{Band: meta.Band, UniverseSize: meta.UniverseSize, RankBy: meta.RankBy, AsOf: meta.AsOf, Raw: raw, Items: items}, nil
}

func FilterTradFiItems(items []DirectionChangeItem, marketType string, limit int) []DirectionChangeItem {
	if marketType == "" {
		marketType = DefaultMarketType
	}
	return filterSignalRankingItems(items, marketType, limit, false)
}

func FilterDirectionChangeItems(items []DirectionChangeItem, marketType string, limit int) []DirectionChangeItem {
	return filterSignalRankingItems(items, marketType, limit, true)
}

func filterSignalRankingItems(items []DirectionChangeItem, marketType string, limit int, allowAll bool) []DirectionChangeItem {
	requestedMarketType := marketType
	normalizedMarketType := normalizeMarketType(marketType)
	includeAll := allowAll && isAllMarketType(marketType)
	if limit <= 0 {
		limit = 5
	}
	if limit > MaxDirectionChangeItems {
		limit = MaxDirectionChangeItems
	}

	out := make([]DirectionChangeItem, 0, limit)
	seen := make(map[string]bool)
	for _, item := range items {
		base := QuerySymbol(item.Symbol)
		if base == "" {
			continue
		}
		itemMarket := normalizeMarketType(item.MarketType)
		isXYZ := hyperliquid.IsXYZAsset(item.Symbol) || hyperliquid.IsXYZAsset(base)
		if !includeAll {
			if itemMarket != "" && normalizedMarketType != "" && itemMarket != normalizedMarketType && !isTradeFiMarketType(itemMarket) && !isXYZ {
				continue
			}
			if itemMarket == "" && !isXYZ {
				continue
			}
		}
		item.MarketType = coalesce(item.MarketType, inferRankingMarketType(item.Symbol, base, requestedMarketType))
		tradeSymbol := TradableSymbolForMarket(item.MarketType, item.Symbol)
		if tradeSymbol == "" || seen[tradeSymbol] {
			continue
		}
		item.Symbol = base
		item.Category = rankingCategory(item.MarketType, base)
		out = append(out, item)
		seen[tradeSymbol] = true
		if len(out) >= limit {
			break
		}
	}
	return out
}

func TradableSymbol(symbol string) string {
	return TradableSymbolForMarket(DefaultMarketType, symbol)
}

func TradableSymbolForMarket(marketType, symbol string) string {
	base := QuerySymbol(symbol)
	if base == "" {
		return ""
	}
	if isCoreMarketType(marketType) {
		return base
	}
	if isAllMarketType(marketType) && !hyperliquid.IsXYZAsset(symbol) && !hyperliquid.IsXYZAsset(base) {
		return base
	}
	return hyperliquid.FormatCoinForAPI("xyz:" + base)
}

func MarketSymbol(marketType, symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	if strings.Contains(symbol, "/") {
		parts := strings.Split(symbol, "/")
		symbol = parts[len(parts)-1]
	}
	if strings.HasPrefix(strings.ToLower(symbol), "xyz:") {
		return "xyz:" + hyperliquid.NormalizeCoinBase(strings.TrimPrefix(strings.ToUpper(symbol), "XYZ:"))
	}
	base := QuerySymbol(symbol)
	if base == "" {
		return ""
	}
	if normalizeMarketType(marketType) == "hip3perp" {
		return "xyz:" + base
	}
	return base
}

func QuerySymbol(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	symbol = strings.TrimPrefix(strings.ToUpper(symbol), "XYZ:")
	if strings.Contains(symbol, "/") {
		parts := strings.Split(symbol, "/")
		symbol = parts[len(parts)-1]
	}
	return hyperliquid.NormalizeCoinBase(symbol)
}

func QueryChain(chain string) string {
	raw := strings.TrimSpace(chain)
	normalized := strings.ToLower(raw)
	switch normalized {
	case "", "hyperliquid", "hl":
		return DefaultChain
	default:
		return raw
	}
}

func FormatAnalysisForAI(analysis *MarketAnalysis) string {
	if analysis == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### %s (Vergex %s/%s)\n", analysis.Symbol, analysis.MarketType, analysis.QuerySymbol))
	if analysis.Ranking != nil {
		sb.WriteString(fmt.Sprintf("Direction leaderboard: rank=%d bias=%s direction_score=%.0f bulls=%d bears=%d neutral=%d oi_rank=%d mark_price=%s category=%s\n",
			analysis.Ranking.Rank,
			emptyDash(analysis.Ranking.Bias),
			analysis.Ranking.Score,
			analysis.Ranking.BullishCount,
			analysis.Ranking.BearishCount,
			analysis.Ranking.NeutralCount,
			analysis.Ranking.OIRank,
			trimFloat(analysis.Ranking.MarkPrice, 6),
			emptyDash(analysis.Ranking.Category)))
	}
	if len(analysis.DirectionCurrent) > 0 {
		sb.WriteString("#### Current Bull/Bear Direction\n")
		sb.WriteString(fallbackJSONBlock(analysis.DirectionCurrent, 2200))
		sb.WriteString("\n")
	} else if analysis.DirectionCurrentError != "" {
		sb.WriteString("Current direction: unavailable (")
		sb.WriteString(truncateText(analysis.DirectionCurrentError, 360))
		sb.WriteString(")\n")
	}
	if len(analysis.DirectionHistory) > 0 {
		sb.WriteString("#### Bull/Bear Direction History\n")
		sb.WriteString(fallbackJSONBlock(analysis.DirectionHistory, 2600))
		sb.WriteString("\n")
	} else if analysis.DirectionHistoryError != "" {
		sb.WriteString("Direction history: unavailable (")
		sb.WriteString(truncateText(analysis.DirectionHistoryError, 360))
		sb.WriteString(")\n")
	}
	if len(analysis.Heatmap) > 0 {
		sb.WriteString("#### Cost/Liquidation Heatmap\n")
		sb.WriteString(FormatHeatmapMarkdown(analysis.Heatmap))
		sb.WriteString("\n")
	} else if analysis.HeatmapError != "" {
		sb.WriteString("Cost/Liquidation Heatmap: unavailable (")
		sb.WriteString(truncateText(analysis.HeatmapError, 360))
		sb.WriteString(")\n")
	}
	return sb.String()
}

func FormatHeatmapMarkdown(raw json.RawMessage) string {
	data, ok := decodeVergexDataObject(raw)
	if !ok {
		return fallbackJSONBlock(raw, 2600)
	}

	bins := objectArray(data, "bins")
	if len(bins) == 0 {
		var sb strings.Builder
		writeScalarSummary(&sb, data, []string{"symbol", "marketType", "band", "liqBand", "currentPrice", "price", "binStep"})
		return withFallbackIfEmpty(sb.String(), raw)
	}

	zones := make([]heatmapZone, 0, len(bins))
	var totalLongCost, totalShortCost, totalLongLiq, totalShortLiq float64
	for _, bin := range bins {
		zone := heatmapZone{
			Start:     firstFloat(bin, "bucketStartPrice", "start", "startPrice"),
			End:       firstFloat(bin, "bucketEndPrice", "end", "endPrice"),
			PX:        firstFloat(bin, "px", "price"),
			LongCost:  firstFloat(bin, "longCost"),
			ShortCost: firstFloat(bin, "shortCost"),
			LongLiq:   firstFloat(bin, "longLiq", "longLiquidation"),
			ShortLiq:  firstFloat(bin, "shortLiq", "shortLiquidation"),
		}
		totalLongCost += zone.LongCost
		totalShortCost += zone.ShortCost
		totalLongLiq += zone.LongLiq
		totalShortLiq += zone.ShortLiq
		zone.Score = maxFloat(zone.LongCost, zone.ShortCost, zone.LongLiq, zone.ShortLiq)
		if zone.Score > 0 {
			zones = append(zones, zone)
		}
	}
	sortHeatmapZones(zones)

	var sb strings.Builder
	writeScalarSummary(&sb, data, []string{"symbol", "marketType", "band", "liqBand", "currentPrice", "price", "binStep"})
	sb.WriteString(fmt.Sprintf("- Total cost: long %s / short %s\n", formatUSDAmount(totalLongCost), formatUSDAmount(totalShortCost)))
	sb.WriteString(fmt.Sprintf("- Total liquidation: long %s / short %s\n", formatUSDAmount(totalLongLiq), formatUSDAmount(totalShortLiq)))
	sb.WriteString("| Price zone | Long cost | Short cost | Long liq | Short liq | Main cluster |\n")
	sb.WriteString("| --- | ---: | ---: | ---: | ---: | --- |\n")
	limit := minInt(len(zones), 10)
	for _, zone := range zones[:limit] {
		sb.WriteString("| ")
		sb.WriteString(markdownCell(formatPriceZone(zone)))
		sb.WriteString(" | ")
		sb.WriteString(markdownCell(formatUSDAmount(zone.LongCost)))
		sb.WriteString(" | ")
		sb.WriteString(markdownCell(formatUSDAmount(zone.ShortCost)))
		sb.WriteString(" | ")
		sb.WriteString(markdownCell(formatUSDAmount(zone.LongLiq)))
		sb.WriteString(" | ")
		sb.WriteString(markdownCell(formatUSDAmount(zone.ShortLiq)))
		sb.WriteString(" | ")
		sb.WriteString(markdownCell(zone.MainCluster()))
		sb.WriteString(" |\n")
	}
	if len(zones) > limit {
		sb.WriteString(fmt.Sprintf("- Additional heatmap bins omitted: %d\n", len(zones)-limit))
	}
	return withFallbackIfEmpty(sb.String(), raw)
}

type heatmapZone struct {
	Start     float64
	End       float64
	PX        float64
	LongCost  float64
	ShortCost float64
	LongLiq   float64
	ShortLiq  float64
	Score     float64
}

func (z heatmapZone) MainCluster() string {
	maxVal := maxFloat(z.LongCost, z.ShortCost, z.LongLiq, z.ShortLiq)
	switch maxVal {
	case z.LongCost:
		return "long cost"
	case z.ShortCost:
		return "short cost"
	case z.LongLiq:
		return "long liquidation"
	case z.ShortLiq:
		return "short liquidation"
	default:
		return "-"
	}
}

func sortHeatmapZones(zones []heatmapZone) {
	sort.SliceStable(zones, func(i, j int) bool {
		return zones[i].Score > zones[j].Score
	})
}

func decodeVergexDataObject(raw json.RawMessage) (map[string]any, bool) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false
	}
	obj, ok := decoded.(map[string]any)
	if !ok {
		return nil, false
	}
	if data, ok := lookupNormalized(obj, "data"); ok {
		if dataObj, ok := data.(map[string]any); ok {
			return dataObj, true
		}
	}
	return obj, true
}

func writeScalarSummary(sb *strings.Builder, obj map[string]any, keys []string) {
	wrote := false
	for _, key := range keys {
		value, ok := lookupNormalized(obj, key)
		if !ok {
			continue
		}
		text := formatScalarValue(value)
		if text == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", titleKey(key), text))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

func objectArray(obj map[string]any, key string) []map[string]any {
	val, ok := lookupNormalized(obj, key)
	if !ok {
		return nil
	}
	rows, ok := val.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if rowObj, ok := row.(map[string]any); ok {
			out = append(out, rowObj)
		}
	}
	return out
}

func formatOptionalFloat(obj map[string]any, key string) string {
	val, ok := lookupNormalized(obj, key)
	if !ok {
		return "-"
	}
	num, ok := anyFloat(val)
	if !ok {
		return formatScalarValue(val)
	}
	return trimFloat(num, 1)
}

func anyFloat(val any) (float64, bool) {
	switch t := val.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func formatScalarValue(val any) string {
	switch t := val.(type) {
	case string:
		return strings.TrimSpace(t)
	case bool:
		return fmt.Sprintf("%t", t)
	case float64:
		return trimFloat(t, 4)
	case json.Number:
		f, err := t.Float64()
		if err == nil {
			return trimFloat(f, 4)
		}
		return t.String()
	default:
		if f, ok := anyFloat(val); ok {
			return trimFloat(f, 4)
		}
		return ""
	}
}

func formatPriceZone(z heatmapZone) string {
	if z.Start != 0 || z.End != 0 {
		return fmt.Sprintf("%s-%s", trimFloat(z.Start, 4), trimFloat(z.End, 4))
	}
	if z.PX != 0 {
		return trimFloat(z.PX, 4)
	}
	return "-"
}

func formatUSDAmount(v float64) string {
	abs := math.Abs(v)
	sign := ""
	if v < 0 {
		sign = "-"
	}
	switch {
	case abs >= 1_000_000_000:
		return fmt.Sprintf("%s$%.2fB", sign, abs/1_000_000_000)
	case abs >= 1_000_000:
		return fmt.Sprintf("%s$%.2fM", sign, abs/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%s$%.2fK", sign, abs/1_000)
	default:
		return fmt.Sprintf("%s$%.2f", sign, abs)
	}
}

func trimFloat(v float64, precision int) string {
	text := fmt.Sprintf("%.*f", precision, v)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	if text == "-0" {
		return "0"
	}
	return text
}

func markdownCell(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	text = strings.ReplaceAll(text, "|", "\\|")
	if text == "" {
		return "-"
	}
	return text
}

func titleKey(key string) string {
	switch key {
	case "marketType":
		return "Market type"
	case "liqBand":
		return "Liquidation band"
	case "currentPrice":
		return "Current price"
	case "binStep":
		return "Bin step"
	case "compositeZ":
		return "Composite Z"
	default:
		if key == "" {
			return ""
		}
		return strings.ToUpper(key[:1]) + key[1:]
	}
}

func withFallbackIfEmpty(text string, raw json.RawMessage) string {
	if strings.TrimSpace(text) == "" {
		return fallbackJSONBlock(raw, 2200)
	}
	return text
}

func fallbackJSONBlock(raw json.RawMessage, maxBytes int) string {
	return "```json\n" + CompactJSON(raw, maxBytes) + "\n```\n"
}

func maxFloat(values ...float64) float64 {
	max := 0.0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func CompactJSON(raw json.RawMessage, maxBytes int) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf any
	if err := json.Unmarshal(raw, &buf); err == nil {
		if compact, err := json.Marshal(buf); err == nil {
			raw = compact
		}
	}
	text := string(raw)
	if maxBytes > 0 && len(text) > maxBytes {
		return text[:maxBytes] + "...<truncated>"
	}
	return text
}

func truncateText(text string, maxBytes int) string {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	return text[:maxBytes] + "...<truncated>"
}

func parseRankItem(obj map[string]any, fallbackRank int) (DirectionChangeItem, bool) {
	symbol := firstString(obj, "symbol", "ticker", "base", "coin", "asset", "market", "name")
	if symbol == "" {
		symbol = nestedMarketString(obj, "symbol", "ticker", "base", "coin", "asset", "name")
	}
	if symbol == "" {
		return DirectionChangeItem{}, false
	}
	raw, _ := json.Marshal(obj)
	rank := firstInt(obj, "rank", "ranking", "position")
	if rank <= 0 {
		rank = fallbackRank
	}
	score := firstFloat(obj, "directionScore", "direction_score", "score")
	marketType := firstString(obj, "marketType", "market_type", "venue")
	if marketType == "" {
		marketType = nestedMarketString(obj, "marketType", "market_type", "venue", "type")
	}
	item := DirectionChangeItem{
		Rank:         rank,
		Symbol:       QuerySymbol(symbol),
		APISymbol:    symbol,
		MarketType:   marketType,
		Bias:         firstString(obj, "bias", "direction", "side", "signal"),
		Score:        score,
		BullishCount: firstInt(obj, "bullishCount"),
		BearishCount: firstInt(obj, "bearishCount"),
		NeutralCount: firstInt(obj, "neutralCount"),
		OIRank:       firstInt(obj, "oiRank"),
		MarkPrice:    firstFloat(obj, "markPrice"),
		Raw:          raw,
	}
	if item.Symbol != "" {
		item.Category = hyperliquid.XYZCategory(item.Symbol)
	}
	return item, item.Symbol != ""
}

func nestedMarketString(obj map[string]any, keys ...string) string {
	val, ok := lookupNormalized(obj, "market")
	if !ok {
		return ""
	}
	nested, ok := val.(map[string]any)
	if !ok {
		return ""
	}
	return firstString(nested, keys...)
}

func findObjectArray(v any) []any {
	switch t := v.(type) {
	case []any:
		if arrayLooksLikeRows(t) {
			return t
		}
		for _, item := range t {
			if rows := findObjectArray(item); len(rows) > 0 {
				return rows
			}
		}
	case map[string]any:
		for _, key := range []string{"data", "items", "results", "ranking", "rankings", "rows", "markets", "signals"} {
			if val, ok := lookupNormalized(t, key); ok {
				if rows := findObjectArray(val); len(rows) > 0 {
					return rows
				}
			}
		}
		for _, val := range t {
			if rows := findObjectArray(val); len(rows) > 0 {
				return rows
			}
		}
	}
	return nil
}

func arrayLooksLikeRows(rows []any) bool {
	for _, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if firstString(obj, "symbol", "ticker", "base", "coin", "asset", "market", "name") != "" {
			return true
		}
	}
	return false
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		val, ok := lookupNormalized(obj, key)
		if !ok {
			continue
		}
		switch t := val.(type) {
		case string:
			if strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		case fmt.Stringer:
			if strings.TrimSpace(t.String()) != "" {
				return strings.TrimSpace(t.String())
			}
		}
	}
	return ""
}

func firstFloat(obj map[string]any, keys ...string) float64 {
	for _, key := range keys {
		val, ok := lookupNormalized(obj, key)
		if !ok {
			continue
		}
		switch t := val.(type) {
		case float64:
			return t
		case int:
			return float64(t)
		case json.Number:
			f, _ := t.Float64()
			return f
		case string:
			var f float64
			if _, err := fmt.Sscanf(strings.TrimSpace(t), "%f", &f); err == nil {
				return f
			}
		}
	}
	return 0
}

func firstInt(obj map[string]any, keys ...string) int {
	for _, key := range keys {
		val, ok := lookupNormalized(obj, key)
		if !ok {
			continue
		}
		switch t := val.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case json.Number:
			i, _ := t.Int64()
			return int(i)
		case string:
			var i int
			if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &i); err == nil {
				return i
			}
		}
	}
	return 0
}

func lookupNormalized(obj map[string]any, key string) (any, bool) {
	want := normalizeKey(key)
	for k, v := range obj {
		if normalizeKey(k) == want {
			return v, true
		}
	}
	return nil, false
}

func normalizeKey(key string) string {
	replacer := strings.NewReplacer("_", "", "-", "", " ", "", ".", "")
	return replacer.Replace(strings.ToLower(strings.TrimSpace(key)))
}

func normalizeMarketType(marketType string) string {
	replacer := strings.NewReplacer("_", "", "-", "", " ", "", ".", "", "/", "")
	return replacer.Replace(strings.ToLower(strings.TrimSpace(marketType)))
}

func isTradeFiMarketType(marketType string) bool {
	switch normalizeMarketType(marketType) {
	case "hip3perp", "hip3", "xyz", "xyzperp", "tradefi", "tradfi",
		"stock", "stocks", "equity", "equities", "usequity", "usequities", "usstock", "usstocks",
		"commodity", "commodities", "forex", "fx", "index", "indices", "preipo":
		return true
	default:
		return false
	}
}

func isAllMarketType(marketType string) bool {
	switch normalizeMarketType(marketType) {
	case "", "all", "any", "ranking", "signalranking", "claw402", "vergex":
		return true
	default:
		return false
	}
}

func isCoreMarketType(marketType string) bool {
	switch normalizeMarketType(marketType) {
	case "coreperp", "core", "crypto", "cryptoperp":
		return true
	default:
		return false
	}
}

func inferRankingMarketType(symbol, base, fallback string) string {
	if !isAllMarketType(fallback) && strings.TrimSpace(fallback) != "" {
		return fallback
	}
	if hyperliquid.IsXYZAsset(symbol) || hyperliquid.IsXYZAsset(base) {
		return DefaultMarketType
	}
	return "core_perp"
}

func rankingCategory(marketType, base string) string {
	if isCoreMarketType(marketType) {
		return "crypto"
	}
	return hyperliquid.XYZCategory(base)
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
