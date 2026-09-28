package api

import (
	"context"
	"fmt"
	"net/http"
	"nofx/logger"
	"nofx/provider/vergex"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleVergexDirectionChangeLeaderboard(c *gin.Context) {
	client, ok := s.newVergexClientForRequest(c)
	if !ok {
		return
	}
	data, err := client.GetDirectionChangeLeaderboard(c.Request.Context())
	if err != nil {
		logger.Warnf("Vergex direction-change leaderboard failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", data.Raw)
}

func (s *Server) handleVergexDirectionChangeCurrent(c *gin.Context) {
	client, ok := s.newVergexClientForRequest(c)
	if !ok {
		return
	}
	symbol := strings.TrimSpace(c.Query("symbol"))
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol is required"})
		return
	}
	body, err := client.GetDirectionChangeCurrent(c.Request.Context(), symbol)
	if err != nil {
		logger.Warnf("Vergex direction-change current failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (s *Server) handleVergexDirectionChangeHistory(c *gin.Context) {
	client, ok := s.newVergexClientForRequest(c)
	if !ok {
		return
	}
	symbol := strings.TrimSpace(c.Query("symbol"))
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol is required"})
		return
	}
	body, err := client.GetDirectionChangeHistory(
		c.Request.Context(), symbol, strings.TrimSpace(c.Query("type")),
		parsePositiveInt(c.Query("page"), 1), parsePositiveInt(c.Query("page_size"), 20),
	)
	if err != nil {
		if strings.Contains(err.Error(), "type must be") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		logger.Warnf("Vergex direction-change history failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (s *Server) handleVergexCostLiquidationHeatmap(c *gin.Context) {
	client, ok := s.newVergexClientForRequest(c)
	if !ok {
		return
	}
	body, err := client.GetCostLiquidationHeatmap(context.Background(), vergex.Query{
		MarketType: withDefault(strings.TrimSpace(c.Query("marketType")), vergex.DefaultMarketType),
		Symbol:     strings.TrimSpace(c.Query("symbol")),
		Chain:      strings.TrimSpace(c.Query("chain")),
		LiqBand:    strings.TrimSpace(c.Query("liqBand")),
	})
	if err != nil {
		logger.Warnf("Vergex cost-liquidation-heatmap failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// handleVergexFlowMarkets proxies the Vergex net-flow market ranking (paid x402
// endpoint) using the caller's claw402 wallet. The upstream JSON is passed
// through verbatim: { data: { window, by, inflow: [{ symbol, netFlow,
// buyNotional, sellNotional, trades, latestPrice }, ...] } }.
func (s *Server) handleVergexFlowMarkets(c *gin.Context) {
	client, ok := s.newVergexClientForRequest(c)
	if !ok {
		return
	}
	chain := withDefault(strings.TrimSpace(c.Query("chain")), "mainnet")
	window := withDefault(strings.TrimSpace(c.Query("window")), "1h")
	limit := parsePositiveInt(c.Query("limit"), 25)

	body, err := client.GetFlowMarkets(context.Background(), chain, window, limit)
	if err != nil {
		logger.Warnf("Vergex flow-markets failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (s *Server) newVergexClientForRequest(c *gin.Context) (*vergex.Client, bool) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return nil, false
	}
	walletKey, err := s.resolveStrategyDataWalletKey(userID, c.Query("ai_model_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if walletKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "claw402 wallet is not configured"})
		return nil, false
	}
	client, err := vergex.NewClient("", walletKey, &logger.MCPLogger{})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	return client, true
}

func parsePositiveInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func withDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
