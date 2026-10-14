package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/eth/ethconfig"
)

func TestUSDBMinerAnchorGapTOMLDefaultsAndRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       uint32
		bad        bool
	}{
		{"missing", "[Eth.Miner.USDB]\n", 2, false},
		{"old miner table", "[Eth.Miner]\nGasCeil = 30000000\n", 2, false},
		{"zero", "[Eth.Miner.USDB]\nAnchorGap = 0\n", 0, false},
		{"override", "[Eth.Miner.USDB]\nAnchorGap = 5\n", 5, false},
		{"negative", "[Eth.Miner.USDB]\nAnchorGap = -1\n", 0, true},
		{"overflow", "[Eth.Miner.USDB]\nAnchorGap = 4294967296\n", 0, true},
		{"fraction", "[Eth.Miner.USDB]\nAnchorGap = 1.5\n", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.text), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := gethConfig{Eth: ethconfig.Defaults}
			err := loadConfig(path, &cfg)
			if test.bad {
				if err == nil {
					t.Fatal("invalid gap accepted")
				}
				return
			}
			if err != nil || cfg.Eth.Miner.USDB.EffectiveAnchorGap() != test.want {
				t.Fatalf("gap=%d err=%v", cfg.Eth.Miner.USDB.EffectiveAnchorGap(), err)
			}
			var presence gethConfigPresence
			if err := loadConfigPresence(path, &presence); err != nil {
				t.Fatalf("valid mining config failed presence projection: %v", err)
			}
			encoded, err := tomlSettings.Marshal(&cfg.Eth.Miner.USDB)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == 0 && !strings.Contains(string(encoded), "AnchorGap = 0") {
				t.Fatal("explicit zero lost on save")
			}
			if err := os.WriteFile(path, append([]byte("[Eth.Miner.USDB]\n"), encoded...), 0600); err != nil {
				t.Fatal(err)
			}
			roundtrip := gethConfig{Eth: ethconfig.Defaults}
			if err := loadConfig(path, &roundtrip); err != nil || roundtrip.Eth.Miner.USDB.EffectiveAnchorGap() != test.want {
				t.Fatalf("roundtrip gap=%d err=%v", roundtrip.Eth.Miner.USDB.EffectiveAnchorGap(), err)
			}
		})
	}
}

func TestUSDBMiningConfigPresenceKeepsStrictValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	input := "[Eth.Miner.USDB]\nAnchorGap = 0\n[Node.P2P]\nListenAddr = ':31303'\nMaxPeers = 10\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := gethConfig{Eth: ethconfig.Defaults}
	if err := loadConfig(path, &cfg); err != nil {
		t.Fatal(err)
	}
	var presence gethConfigPresence
	if err := loadConfigPresence(path, &presence); err != nil {
		t.Fatal(err)
	}
	if presence.Node.P2P.ListenAddr == nil || *presence.Node.P2P.ListenAddr != ":31303" {
		t.Fatal("listen address presence lost")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(input, "AnchorGap", "UnknownAnchorGap", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(path, &cfg); err == nil {
		t.Fatal("unknown full-config field accepted")
	}
}
