package store

import "testing"

func TestDefaultVergexStrategyDoesNotEnableNofxOSData(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	assertVergexSignalDefault(t, cfg)
	ind := cfg.Indicators
	if ind.NofxOSAPIKey != "" {
		t.Fatalf("default should not include a NofxOS API key for Claw402/Vergex strategies")
	}
	if ind.EnableQuantData || ind.EnableQuantOI || ind.EnableQuantNetflow || ind.EnableOIRanking || ind.EnableNetFlowRanking || ind.EnablePriceRanking {
		t.Fatalf("default Claw402/Vergex strategy must not enable NofxOS datasets: %+v", ind)
	}
	if !ind.EnableRawKlines {
		t.Fatalf("raw Hyperliquid klines must stay enabled")
	}
}

func TestVergexSignalDefaultSurvivesClampAndNormalize(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource.UseAI500 = true
	cfg.ClampLimits()
	assertVergexSignalDefault(t, cfg)
	if cfg.CoinSource.UseAI500 {
		t.Fatalf("Claw402/Vergex signal strategy must clear stale AI500 flag: %+v", cfg.CoinSource)
	}
}

func TestEmptyCoinSourceInfersVergexSignalNotAI500(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource = CoinSourceConfig{}
	cfg.NormalizeProductSchema()
	assertVergexSignalDefault(t, cfg)
}

func assertVergexSignalDefault(t *testing.T, cfg StrategyConfig) {
	t.Helper()
	if cfg.CoinSource.SourceType != "vergex_signal" || cfg.CoinSource.VergexLimit != 10 || cfg.CoinSource.VergexMarketType != "all" || cfg.CoinSource.VergexChain != "hyperliquid" {
		t.Fatalf("coin source = %+v, want Claw402/Vergex all-market signal top 10", cfg.CoinSource)
	}
}
