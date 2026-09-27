package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/internal/peercheck"
	fixture "github.com/ethereum/go-ethereum/tests/common/peercheck"
)

func TestPeerCheckRequestValidation(t *testing.T) {
	req := fixture.Request()
	req.Enode = "enode://bad@localhost:31303"
	data, _ := json.Marshal(req)
	var output bytes.Buffer
	if err := runPeerCheck(context.Background(), bytes.NewReader(data), &output); err != nil {
		t.Fatal(err)
	}
	var report peercheck.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Usable || report.Syntax.State != "FAIL" {
		t.Fatalf("malformed enode produced success: %s, %v", output.Bytes(), err)
	}
	for _, input := range []string{"{}", string(data) + " {}", "{\"unexpected\":true}"} {
		if err := runPeerCheck(context.Background(), strings.NewReader(input), &output); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
