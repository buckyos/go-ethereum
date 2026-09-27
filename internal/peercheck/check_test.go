package peercheck

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

func TestResolveFailureAndFiltering(t *testing.T) {
	key, _ := crypto.GenerateKey()
	n := enode.NewV4(&key.PublicKey, net.IPv4(127, 0, 0, 1), 31303, 31303)
	dns, err := enode.ParseForConfig(enode.ValidSchemes, "enode://"+n.URLv4()[8:136]+"@offline.invalid:31303")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolve(context.Background(), dns, func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("NXDOMAIN") })
	if err == nil {
		t.Fatal("DNS failure hidden")
	}
	ips, _, err := resolve(context.Background(), dns, func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("::")}, {IP: net.ParseIP("ff02::1")},
			{IP: net.ParseIP("fe80::1")}, {IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	})
	if err != nil || len(ips) != 1 || !ips[0].IsLoopback() {
		t.Fatalf("invalid addresses accepted: %v %v", ips, err)
	}
}
