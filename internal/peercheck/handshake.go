package peercheck

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/forkid"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/rlpx"
	"github.com/ethereum/go-ethereum/rlp"
)

// hello uses the devp2p base protocol's wire format. Advertise only eth, so its
// messages always begin at offset 16 and no unrelated subprotocol is started.
type hello struct {
	Version    uint64
	Name       string
	Caps       []p2p.Cap
	ListenPort uint64
	ID         []byte
	Rest       []rlp.RawValue `rlp:"tail"`
}

func handshake(ctx context.Context, fd net.Conn, n *enode.Node, key *ecdsa.PrivateKey, req Request, e *Endpoint) {
	deadline, _ := ctx.Deadline()
	fd.SetDeadline(deadline)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			fd.Close()
		case <-done:
		}
	}()
	conn := rlpx.NewConn(fd, n.Pubkey())
	remoteKey, err := conn.Handshake(key)
	if err != nil {
		e.Identity = failed("RLPX_IDENTITY_FAILED", err)
		return
	}
	if enode.PubkeyToIDV4(remoteKey) != n.ID() {
		e.Identity = failed("RLPX_IDENTITY_FAILED", errors.New("remote public key does not match enode"))
		return
	}
	e.Identity = pass()
	ours := hello{Version: 5, Name: "usdb-peer-check", ID: crypto.FromECDSAPub(&key.PublicKey)[1:]}
	for _, version := range eth.ProtocolVersions {
		ours.Caps = append(ours.Caps, p2p.Cap{Name: "eth", Version: version})
	}
	outgoing, _ := rlp.EncodeToBytes(ours)
	written := make(chan error, 1)
	go func() { _, err := conn.Write(0, outgoing); written <- err }()
	code, data, _, readErr := conn.Read()
	if readErr != nil {
		fd.Close()
	}
	writeErr := <-written
	if readErr != nil {
		e.Hello = failed("HELLO_FAILED", readErr)
		return
	}
	if writeErr != nil {
		e.Hello = failed("HELLO_FAILED", writeErr)
		return
	}
	if code == 1 {
		e.Hello = failed("PEER_DISCONNECTED", disconnectReason(data))
		return
	}
	if code != 0 || len(data) > 2048 {
		e.Hello = failed("HELLO_FAILED", errors.New("invalid hello message"))
		return
	}
	var remote hello
	if err := rlp.DecodeBytes(data, &remote); err != nil {
		e.Hello = failed("HELLO_FAILED", err)
		return
	}
	if !bytes.Equal(remote.ID, crypto.FromECDSAPub(n.Pubkey())[1:]) {
		e.Hello = failed("HELLO_IDENTITY_MISMATCH", errors.New("hello public key does not match enode"))
		return
	}
	version := uint(0)
	for _, cap := range remote.Caps {
		for _, supported := range eth.ProtocolVersions {
			if cap.Name == "eth" && cap.Version == supported && cap.Version > version {
				version = cap.Version
			}
		}
	}
	if version == 0 {
		e.Hello = failed("NO_SHARED_ETH_PROTOCOL", errors.New("peer does not support this client's eth protocol"))
		return
	}
	e.Hello = Check{State: "PASS", Detail: fmt.Sprintf("eth/%d", version)}
	conn.SetSnappy(remote.Version >= 5)
	// Reuse the client's actual eth handshake checks for network ID, genesis,
	// protocol version, fork compatibility and total difficulty validation.
	rw := &statusRW{conn: conn}
	peer := eth.NewPeer(version, p2p.NewPeer(n.ID(), remote.Name, remote.Caps), rw, nil)
	defer peer.Close()
	err = peer.Handshake(req.NetworkID, req.Genesis.Difficulty, req.GenesisHash, req.GenesisHash,
		forkid.NewID(req.Genesis.Config, req.GenesisHash, 0), forkid.NewStaticFilter(req.Genesis.Config, req.GenesisHash))
	if err != nil {
		e.ETH = failed("ETH_HANDSHAKE_FAILED", err)
		return
	}
	e.ETH = pass()
}

func disconnectReason(data []byte) error {
	var reason []p2p.DiscReason
	if err := rlp.DecodeBytes(data, &reason); err != nil || len(reason) != 1 {
		return errors.New("peer disconnected without a valid reason")
	}
	return fmt.Errorf("peer disconnected: %s", reason[0])
}

// statusRW adapts the RLPx stream to the existing eth handshake and handles base
// protocol liveness messages without participating in block or tx exchange.
type statusRW struct {
	conn *rlpx.Conn
	wmu  sync.Mutex
}

func (rw *statusRW) ReadMsg() (p2p.Msg, error) {
	for count := 0; count < 8; count++ {
		code, data, _, err := rw.conn.Read()
		if err != nil {
			return p2p.Msg{}, err
		}
		if len(data) > 2048 {
			return p2p.Msg{}, errors.New("oversized handshake message")
		}
		switch code {
		case 1:
			return p2p.Msg{}, disconnectReason(data)
		case 2:
			rw.wmu.Lock()
			_, err = rw.conn.Write(3, []byte{0xc0})
			rw.wmu.Unlock()
			if err != nil {
				return p2p.Msg{}, err
			}
		case 3:
			continue
		default:
			if code < 16 {
				return p2p.Msg{}, errors.New("unexpected base protocol message")
			}
			return p2p.Msg{Code: code - 16, Size: uint32(len(data)), Payload: bytes.NewReader(data), ReceivedAt: time.Now()}, nil
		}
	}
	return p2p.Msg{}, errors.New("too many messages before eth status")
}

func (rw *statusRW) WriteMsg(msg p2p.Msg) error {
	data, err := io.ReadAll(io.LimitReader(msg.Payload, 2049))
	if err != nil {
		return err
	}
	if len(data) > 2048 {
		return errors.New("oversized outgoing handshake message")
	}
	rw.wmu.Lock()
	defer rw.wmu.Unlock()
	_, err = rw.conn.Write(msg.Code+16, data)
	return err
}
