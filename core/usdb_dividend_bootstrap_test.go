package core_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/abi/bind/backends"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

type hardhatArtifact struct {
	ABI              json.RawMessage `json:"abi"`
	DeployedBytecode string          `json:"deployedBytecode"`
}

func loadSourceDAOArtifact(t *testing.T, relative string) (abi.ABI, []byte) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file path")
	}
	artifactPath := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "SourceDAO", relative))
	blob, err := os.ReadFile(artifactPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("sourceDAO artifact is not available at %s", artifactPath)
		}
		t.Fatalf("failed to read artifact %s: %v", artifactPath, err)
	}
	var artifact hardhatArtifact
	if err := json.Unmarshal(blob, &artifact); err != nil {
		t.Fatalf("failed to decode artifact %s: %v", artifactPath, err)
	}
	parsedABI, err := abi.JSON(strings.NewReader(string(artifact.ABI)))
	if err != nil {
		t.Fatalf("failed to parse abi %s: %v", artifactPath, err)
	}
	code := common.FromHex(artifact.DeployedBytecode)
	if len(code) == 0 {
		t.Fatalf("artifact %s has empty deployed bytecode", artifactPath)
	}
	return parsedABI, code
}

func newSourceDAOTransactor(t *testing.T, keyHex string, chainID *big.Int) *bind.TransactOpts {
	t.Helper()

	key, err := parseSourceDAOTestKey(keyHex)
	if err != nil {
		t.Fatalf("failed to parse bootstrap private key: %v", err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(key, chainID)
	if err != nil {
		t.Fatalf("failed to create transact opts: %v", err)
	}
	auth.Context = context.Background()
	auth.GasLimit = 8_000_000
	return auth
}

func parseSourceDAOTestKey(keyHex string) (*ecdsa.PrivateKey, error) {
	return crypto.HexToECDSA(keyHex)
}

func mustCommitTx(t *testing.T, backend *backends.SimulatedBackend, tx *types.Transaction) {
	t.Helper()

	backend.Commit()
	receipt, err := backend.TransactionReceipt(context.Background(), tx.Hash())
	if err != nil {
		t.Fatalf("failed to fetch receipt %s: %v", tx.Hash(), err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("transaction %s failed with status %d", tx.Hash(), receipt.Status)
	}
}

func mustPreflightCall(t *testing.T, backend *backends.SimulatedBackend, from common.Address, to common.Address, input []byte) {
	t.Helper()

	_, err := backend.CallContract(context.Background(), ethereum.CallMsg{
		From:     from,
		To:       &to,
		Gas:      8_000_000,
		GasPrice: big.NewInt(params.InitialBaseFee),
		Data:     input,
	}, nil)
	if err == nil {
		return
	}
	var dataErr interface{ ErrorData() interface{} }
	if errors.As(err, &dataErr) {
		if revertData, ok := dataErr.ErrorData().(string); ok {
			if unpacked, unpackErr := abi.UnpackRevert(common.FromHex(revertData)); unpackErr == nil {
				t.Fatalf("preflight call reverted: %s", unpacked)
			}
			t.Fatalf("preflight call reverted with raw data %s: %v", revertData, err)
		}
	}
	t.Fatalf("preflight call failed: %v", err)
}

func readAddressCall(t *testing.T, contract *bind.BoundContract, method string) common.Address {
	t.Helper()

	var out []interface{}
	if err := contract.Call(&bind.CallOpts{Context: context.Background()}, &out, method); err != nil {
		t.Fatalf("failed to call %s: %v", method, err)
	}
	if len(out) != 1 {
		t.Fatalf("unexpected %s result count: %d", method, len(out))
	}
	return *abi.ConvertType(out[0], new(common.Address)).(*common.Address)
}

func TestUSDBDividendBootstrapIntegration(t *testing.T) {
	daoABI, daoCode := loadSourceDAOArtifact(t, "artifacts/contracts/Dao.sol/SourceDao.json")
	dividendABI, dividendCode := loadSourceDAOArtifact(t, "artifacts/contracts/Dividend.sol/DividendContract.json")

	daoAddr := common.HexToAddress("0x0000000000000000000000000000000000001001")
	dividendAddr := common.HexToAddress("0x0000000000000000000000000000000000001002")
	bootstrapKeyHex := "4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4f5bcaeaf3f4e6f54"
	key, err := parseSourceDAOTestKey(bootstrapKeyHex)
	if err != nil {
		t.Fatalf("failed to parse bootstrap private key: %v", err)
	}
	bootstrapAddr := crypto.PubkeyToAddress(key.PublicKey)
	depositValue := big.NewInt(1_000_000_000_000_000)
	cycleMinLength := big.NewInt(60)

	genesis := core.DefaultUSDBGenesisBlockWithBootstrap(core.USDBBootstrapGenesisConfig{
		DaoAddress:            daoAddr,
		DaoCode:               daoCode,
		DividendAddress:       dividendAddr,
		DividendCode:          dividendCode,
		BootstrapAdmin:        bootstrapAddr,
		BootstrapAdminBalance: new(big.Int).Mul(big.NewInt(10), big.NewInt(params.Ether)),
		DividendFeeSplitBlock: big.NewInt(16),
	})
	// This fixture exercises only the SourceDAO bootstrap contracts. Consensus
	// profile resolution is covered by ethash tests with an explicit resolver.
	genesis.Config.USDB = nil
	chainID := genesis.Config.EffectiveChainID(big.NewInt(0))
	auth := newSourceDAOTransactor(t, bootstrapKeyHex, chainID)
	backend := backends.NewSimulatedBackendWithGenesis(genesis)
	defer backend.Close()

	dao := bind.NewBoundContract(daoAddr, daoABI, backend, backend, backend)
	dividend := bind.NewBoundContract(dividendAddr, dividendABI, backend, backend, backend)

	initDaoInput, err := daoABI.Pack("initialize")
	if err != nil {
		t.Fatalf("failed to pack dao initialize: %v", err)
	}
	mustPreflightCall(t, backend, auth.From, daoAddr, initDaoInput)
	tx, err := dao.Transact(newSourceDAOTransactor(t, bootstrapKeyHex, chainID), "initialize")
	if err != nil {
		t.Fatalf("failed to initialize dao: %v", err)
	}
	mustCommitTx(t, backend, tx)

	initDividendInput, err := dividendABI.Pack("initialize", cycleMinLength, daoAddr)
	if err != nil {
		t.Fatalf("failed to pack dividend initialize: %v", err)
	}
	mustPreflightCall(t, backend, auth.From, dividendAddr, initDividendInput)
	tx, err = dividend.Transact(newSourceDAOTransactor(t, bootstrapKeyHex, chainID), "initialize", cycleMinLength, daoAddr)
	if err != nil {
		t.Fatalf("failed to initialize dividend: %v", err)
	}
	mustCommitTx(t, backend, tx)

	tx, err = dao.Transact(newSourceDAOTransactor(t, bootstrapKeyHex, chainID), "setTokenDividendAddress", dividendAddr)
	if err != nil {
		t.Fatalf("failed to wire dividend address into dao: %v", err)
	}
	mustCommitTx(t, backend, tx)

	if got := readAddressCall(t, dao, "bootstrapAdmin"); got != auth.From {
		t.Fatalf("unexpected bootstrap admin: %s", got)
	}
	if got := readAddressCall(t, dao, "dividend"); got != dividendAddr {
		t.Fatalf("unexpected dao dividend address: %s", got)
	}

	nonce, err := backend.PendingNonceAt(context.Background(), auth.From)
	if err != nil {
		t.Fatalf("failed to fetch bootstrap admin nonce: %v", err)
	}
	plainTx, err := types.SignTx(
		types.NewTransaction(
			nonce,
			dividendAddr,
			depositValue,
			200_000,
			big.NewInt(params.InitialBaseFee),
			nil,
		),
		types.LatestSignerForChainID(chainID),
		key,
	)
	if err != nil {
		t.Fatalf("failed to sign native transfer to dividend: %v", err)
	}
	if err := backend.SendTransaction(context.Background(), plainTx); err != nil {
		t.Fatalf("failed to send native transfer to dividend: %v", err)
	}
	mustCommitTx(t, backend, plainTx)

	balance, err := backend.BalanceAt(context.Background(), dividendAddr, nil)
	if err != nil {
		t.Fatalf("failed to read dividend balance: %v", err)
	}
	if balance.Cmp(depositValue) < 0 {
		t.Fatalf("unexpected dividend balance: have %v want at least %v", balance, depositValue)
	}
}
