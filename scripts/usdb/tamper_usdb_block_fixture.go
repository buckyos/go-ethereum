package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/usdb"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	fieldPayloadVersion   = "payload_version"
	fieldDifficultyPolicy = "difficulty_policy_version"
	fieldBTCHeight        = "btc_height"
	fieldBTCAnchorAge     = "btc_anchor_age_blocks"
	fieldSnapshotID       = "snapshot_id"
	fieldSystemStateID    = "system_state_id"
	fieldPassID           = "pass_id"
)

func tamperSelector(extra []byte, field string) ([]byte, error) {
	if len(extra) != usdb.ProfileSelectorPayloadV1Size {
		return nil, fmt.Errorf(
			"selector has %d bytes, want %d",
			len(extra),
			usdb.ProfileSelectorPayloadV1Size,
		)
	}
	tampered := append([]byte(nil), extra...)
	if field == fieldPayloadVersion {
		tampered[0] ^= 0xff
		return tampered, nil
	}

	var selector usdb.ProfileSelectorPayload
	if err := selector.UnmarshalBinary(tampered); err != nil {
		return nil, fmt.Errorf("decode selector: %w", err)
	}
	switch field {
	case fieldDifficultyPolicy:
		selector.DifficultyPolicyVersion ^= 0xffff
	case fieldBTCHeight:
		selector.BTCHeight ^= 1
	case fieldBTCAnchorAge:
		selector.BTCAnchorAgeBlocks ^= 1
	case fieldSnapshotID:
		selector.SnapshotID[0] ^= 0xff
	case fieldSystemStateID:
		selector.SystemStateID[0] ^= 0xff
	case fieldPassID:
		selector.PassID.TxID[0] ^= 0xff
	default:
		return nil, fmt.Errorf("unsupported selector field %q", field)
	}
	encoded, err := selector.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("encode tampered selector: %w", err)
	}
	return encoded, nil
}

func rewriteBlockFixture(inputPath, outputPath, field string) error {
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open input fixture: %w", err)
	}
	defer input.Close()

	stream := rlp.NewStream(input, 0)
	var block types.Block
	if err := stream.Decode(&block); err != nil {
		return fmt.Errorf("decode input block: %w", err)
	}
	var trailing types.Block
	if err := stream.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("input fixture contains more than one block")
		}
		return fmt.Errorf("check trailing fixture data: %w", err)
	}

	header := block.Header()
	header.Extra, err = tamperSelector(header.Extra, field)
	if err != nil {
		return err
	}
	tamperedBlock := block.WithSeal(header)

	output, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output fixture: %w", err)
	}
	if err := rlp.Encode(output, tamperedBlock); err != nil {
		output.Close()
		return fmt.Errorf("encode output block: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close output fixture: %w", err)
	}
	return nil
}

func main() {
	inputPath := flag.String("input", "", "single-block RLP fixture to read")
	outputPath := flag.String("output", "", "tampered single-block RLP fixture to write")
	field := flag.String("field", "", "selector field to tamper")
	flag.Parse()

	if *inputPath == "" || *outputPath == "" || *field == "" {
		fmt.Fprintln(os.Stderr, "--input, --output, and --field are required")
		os.Exit(2)
	}
	if err := rewriteBlockFixture(*inputPath, *outputPath, *field); err != nil {
		fmt.Fprintf(os.Stderr, "tamper USDB block fixture: %v\n", err)
		os.Exit(1)
	}
}
