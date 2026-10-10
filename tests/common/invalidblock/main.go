// invalidblock creates one malformed gas header for isolated import acceptance.
// It never signs, submits, or edits a chain database.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

func main() {
	input := flag.String("input", "", "file containing debug_getBlockRlp hex")
	output := flag.String("output", "", "new RLP block file for isolated admin_importChain")
	flag.Parse()
	if err := run(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input, output string) error {
	text, err := ioutil.ReadFile(input)
	if err != nil {
		return err
	}
	encoded, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(string(text)), "0x"))
	if err != nil {
		return err
	}
	var block types.Block
	if err := rlp.DecodeBytes(encoded, &block); err != nil {
		return err
	}
	header := block.Header()
	if header.Number.Sign() <= 0 || header.GasLimit == ^uint64(0) {
		return fmt.Errorf("fixture requires a non-genesis block and incrementable gas limit")
	}
	header.GasUsed = header.GasLimit + 1
	invalid := block.WithSeal(header)
	encoded, err = rlp.EncodeToBytes(invalid)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"hash": invalid.Hash(), "number": invalid.NumberU64()})
}
