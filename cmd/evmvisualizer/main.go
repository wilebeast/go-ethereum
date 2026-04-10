package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/eth/tracers/logger"
	"github.com/holiman/uint256"
)

const defaultYulPath = "docs/evm/store_or_load.yul"

const embeddedYulSource = `object "StoreOrLoad" {
    code {
        datacopy(0, dataoffset("runtime"), datasize("runtime"))
        return(0, datasize("runtime"))
    }

    object "runtime" {
        code {
            switch calldatasize()
            case 0 {
                mstore(0, sload(0))
                return(0, 32)
            }
            default {
                sstore(0, calldataload(0))
                mstore(0, sload(0))
                return(0, 32)
            }
        }
    }
}`

// Embedded bytecode is kept as a fallback so the tool still runs on systems
// without a local solc installation. If solc is available, the program will
// compile store_or_load.yul and prefer the compiler output over these values.
const embeddedRuntimeBytecodeHex = "0x36600f5760005460005260206000f35b60003560005560005460005260206000f3"
const embeddedInitBytecodeHex = "0x6021600c60003960216000f336600f5760005460005260206000f35b60003560005560005460005260206000f3"

type contractSpec struct {
	YulSource       string
	RuntimeBytecode string
	InitBytecode    string
	BuildMode       string
}

// visualizerData is the top-level payload consumed by two outputs:
// 1. trace.json, which is machine-readable and easy to inspect offline
// 2. visualizer.html, which embeds the same payload for browser-side rendering
//
// Input side:
// - collected execution traces from runtime.Call + StructLogger
// - static metadata such as Yul source, bytecode, and source links
//
// Output side:
//   - a single self-contained object that the HTML UI can render without fetching
//     anything else.
type visualizerData struct {
	Title         string         `json:"title"`
	GeneratedAt   string         `json:"generatedAt"`
	Contract      contractData   `json:"contract"`
	ContractAddr  string         `json:"contractAddress"`
	Scenarios     []scenarioData `json:"scenarios"`
	SourceLinks   []sourceLink   `json:"sourceLinks"`
	ManualSummary []manualNote   `json:"manualSummary"`
}

type contractData struct {
	Name            string `json:"name"`
	YulSource       string `json:"yulSource"`
	RuntimeBytecode string `json:"runtimeBytecode"`
	InitBytecode    string `json:"initBytecode"`
	BuildMode       string `json:"buildMode"`
}

// scenarioData describes one transaction-like execution against the deployed
// contract.
//
// InputHex:
// - calldata passed into runtime.Call
//
// ReturnHex:
// - bytes returned by the EVM after executing that calldata
//
// Steps:
// - per-opcode snapshots decoded from Geth's StructLogger output
type scenarioData struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	InputHex      string            `json:"inputHex"`
	ReturnHex     string            `json:"returnHex"`
	Failed        bool              `json:"failed"`
	GasLimit      uint64            `json:"gasLimit"`
	ActualGasUsed uint64            `json:"actualGasUsed"`
	ManualGasUsed uint64            `json:"manualGasUsed"`
	GasDelta      int64             `json:"gasDelta"`
	GasBreakdown  []gasBreakdown    `json:"gasBreakdown"`
	Steps         []stepData        `json:"steps"`
	FinalStorage  map[string]string `json:"finalStorage"`
}

type gasBreakdown struct {
	Op    string `json:"op"`
	Cost  uint64 `json:"cost"`
	Note  string `json:"note"`
	Total uint64 `json:"runningTotal"`
}

type stepData struct {
	Index    int               `json:"index"`
	PC       uint64            `json:"pc"`
	Op       string            `json:"op"`
	Gas      uint64            `json:"gas"`
	GasCost  uint64            `json:"gasCost"`
	Depth    int               `json:"depth"`
	Error    string            `json:"error,omitempty"`
	Stack    []string          `json:"stack"`
	Memory   []string          `json:"memory"`
	Storage  map[string]string `json:"storage"`
	GasUsed  uint64            `json:"gasUsedBefore"`
	GasAfter uint64            `json:"gasAfter"`
}

type sourceLink struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type manualNote struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type traceResult struct {
	Gas         uint64            `json:"gas"`
	Failed      bool              `json:"failed"`
	ReturnValue hexutil.Bytes     `json:"returnValue"`
	StructLogs  []json.RawMessage `json:"structLogs"`
}

// legacyStep matches the legacy JSON shape emitted by StructLogger.
//
// This is not the final UI model. It is an intermediate decode target used to
// translate Geth's trace format into the simpler stepData schema rendered by the
// HTML page.
type legacyStep struct {
	Pc            uint64             `json:"pc"`
	Op            string             `json:"op"`
	Gas           uint64             `json:"gas"`
	GasCost       uint64             `json:"gasCost"`
	Depth         int                `json:"depth"`
	Error         string             `json:"error,omitempty"`
	Stack         *[]string          `json:"stack,omitempty"`
	Memory        *[]string          `json:"memory,omitempty"`
	Storage       *map[string]string `json:"storage,omitempty"`
	RefundCounter uint64             `json:"refund,omitempty"`
}

func main() {
	// Program input:
	// - two optional CLI flags specifying where to write the generated artifacts
	// - an optional Yul source path used when a local solc compiler is available
	//
	// Program output:
	// - a JSON file containing structured trace data
	// - a standalone HTML file embedding the same data as a base64 payload
	yulPath := flag.String("yul", defaultYulPath, "path to the Yul source file to compile when solc is available")
	jsonOut := flag.String("json", "docs/evm/trace.json", "path to write the generated trace JSON")
	htmlOut := flag.String("html", "docs/evm/visualizer.html", "path to write the generated HTML visualizer")
	flag.Parse()

	data, err := buildVisualizerData(*yulPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build visualizer data: %v\n", err)
		os.Exit(1)
	}
	if err := writeJSON(*jsonOut, data); err != nil {
		fmt.Fprintf(os.Stderr, "write json: %v\n", err)
		os.Exit(1)
	}
	if err := writeHTML(*htmlOut, data); err != nil {
		fmt.Fprintf(os.Stderr, "write html: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s and %s\n", *jsonOut, *htmlOut)
}

// buildVisualizerData is the core orchestration function.
//
// Inputs:
//   - no external runtime inputs beyond the fixed contract/bytecode constants in
//     this file
//
// Internal work:
// - creates an in-memory StateDB
// - funds a caller account
// - deploys the contract from initCode
// - executes three scenarios against the deployed runtime code
// - collects actual gas + trace steps + final storage state
// - attaches manually computed gas expectations
//
// Output:
// - one fully-populated visualizerData object used by both JSON and HTML outputs
func buildVisualizerData(yulPath string) (*visualizerData, error) {
	spec, err := loadContractSpec(yulPath)
	if err != nil {
		return nil, err
	}
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		return nil, err
	}
	origin := common.HexToAddress("0x1000000000000000000000000000000000000001")
	statedb.AddBalance(origin, uint256.NewInt(1_000_000_000_000_000_000), tracing.BalanceChangeUnspecified)

	baseCfg := &runtime.Config{
		State:    statedb,
		Origin:   origin,
		GasLimit: 1_000_000,
		GasPrice: common.Big1,
	}

	// Deployment input:
	// - contract init bytecode, which is decoded and executed once
	//
	// Deployment output:
	// - a new contract address containing runtimeBytecodeHex as persistent code
	contractAddr, err := deployContract(baseCfg, spec.InitBytecode)
	if err != nil {
		return nil, err
	}

	scenarios := make([]scenarioData, 0, 3)

	readEmpty, err := traceCall("read_empty", "Read slot0 before any write", "Empty calldata triggers the read branch and returns slot 0.", contractAddr, nil, baseCfg, manualReadColdSload(), statedb)
	if err != nil {
		return nil, err
	}
	scenarios = append(scenarios, readEmpty)

	writeInput := leftPad32([]byte{0x2a})
	writeValue, err := traceCall("write_value", "Write 0x2a into slot0", "A 32-byte calldata word triggers the write branch, stores slot 0, then reads it back.", contractAddr, writeInput, baseCfg, manualZeroToNonZeroSstore(), statedb)
	if err != nil {
		return nil, err
	}
	scenarios = append(scenarios, writeValue)

	readWritten, err := traceCall("read_after_write", "Read slot0 after the write", "A second transaction reads the already-populated slot 0. The slot is cold again because each call is a fresh transaction context.", contractAddr, nil, baseCfg, manualReadColdSload(), statedb)
	if err != nil {
		return nil, err
	}
	scenarios = append(scenarios, readWritten)

	return &visualizerData{
		Title:       "EVM Execution Visualizer",
		GeneratedAt: time.Now().Format(time.RFC3339),
		Contract: contractData{
			Name:            "StoreOrLoad",
			YulSource:       spec.YulSource,
			RuntimeBytecode: spec.RuntimeBytecode,
			InitBytecode:    spec.InitBytecode,
			BuildMode:       spec.BuildMode,
		},
		ContractAddr: contractAddr.Hex(),
		Scenarios:    scenarios,
		SourceLinks: []sourceLink{
			{Label: "EVM interpreter loop", Path: "~/core/vm/interpreter.go"},
			{Label: "Opcode gas tables", Path: "~/core/vm/gas_table.go"},
			{Label: "Opcode definitions", Path: "~/core/vm/opcodes.go"},
			{Label: "Stack implementation", Path: "~/core/vm/stack.go"},
			{Label: "Memory implementation", Path: "~/core/vm/memory.go"},
			{Label: "Struct logger tracer", Path: "~/eth/tracers/logger/logger.go"},
		},
		ManualSummary: []manualNote{
			{Title: "Build mode", Body: spec.BuildMode},
			{Title: "Read path", Body: "CALLDATASIZE, PUSH1, JUMPI, PUSH1, cold SLOAD, PUSH1, MSTORE (3 + 3 memory expansion), PUSH1, PUSH1, RETURN = 2133 gas."},
			{Title: "Write path", Body: "CALLDATASIZE, PUSH1, JUMPI, JUMPDEST, PUSH1, CALLDATALOAD, PUSH1, cold SSTORE zero->nonzero (22100), PUSH1, warm SLOAD (100), PUSH1, MSTORE (3 + 3 memory expansion), PUSH1, PUSH1, RETURN = 22243 gas."},
			{Title: "Transaction boundary", Body: "The second read still pays a cold SLOAD because Geth rebuilds warm access sets per transaction in StateDB.Prepare()."},
		},
	}, nil
}

// loadContractSpec loads the Yul source and prefers compiling it with a local
// solc installation. If solc is not available, the tool falls back to the
// embedded handwritten bytecode so the example remains runnable.
func loadContractSpec(yulPath string) (*contractSpec, error) {
	sourceBytes, err := os.ReadFile(yulPath)
	if err != nil {
		sourceBytes = []byte(embeddedYulSource)
	}
	source := string(sourceBytes)

	_, err = exec.LookPath("solc")
	if err != nil {
		return &contractSpec{
			YulSource:       source,
			RuntimeBytecode: embeddedRuntimeBytecodeHex,
			InitBytecode:    embeddedInitBytecodeHex,
			BuildMode:       "embedded fallback bytecode (solc not found)",
		}, nil
	}
	initBytecode, runtimeBytecode, err := compileYulWithSolc(source)
	if err != nil {
		return nil, err
	}
	return &contractSpec{
		YulSource:       source,
		RuntimeBytecode: runtimeBytecode,
		InitBytecode:    initBytecode,
		BuildMode:       "compiled from Yul source via local solc --standard-json",
	}, nil
}

type standardJSONInput struct {
	Language string                        `json:"language"`
	Sources  map[string]standardJSONSource `json:"sources"`
	Settings standardJSONSettings          `json:"settings"`
}

type standardJSONSource struct {
	Content string `json:"content"`
}

type standardJSONSettings struct {
	OutputSelection map[string]map[string][]string `json:"outputSelection"`
}

type standardJSONOutput struct {
	Contracts map[string]map[string]struct {
		EVM struct {
			Bytecode struct {
				Object string `json:"object"`
			} `json:"bytecode"`
			DeployedBytecode struct {
				Object string `json:"object"`
			} `json:"deployedBytecode"`
		} `json:"evm"`
	} `json:"contracts"`
	Errors []struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func compileYulWithSolc(source string) (initBytecode string, runtimeBytecode string, err error) {
	input := standardJSONInput{
		Language: "Yul",
		Sources: map[string]standardJSONSource{
			"input.yul": {Content: source},
		},
		Settings: standardJSONSettings{
			OutputSelection: map[string]map[string][]string{
				"*": {
					"*": {
						"evm.bytecode.object",
						"evm.deployedBytecode.object",
					},
				},
			},
		},
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return "", "", err
	}
	cmd := exec.Command("solc", "--standard-json")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("solc --standard-json failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	// solc-js may print a non-JSON informational line before the standard-json
	// payload. Trim everything before the first '{' so both native solc and the
	// solc-js wrapper can be parsed by the same decoder.
	outputBytes := stdout.Bytes()
	if idx := bytes.IndexByte(outputBytes, '{'); idx >= 0 {
		outputBytes = outputBytes[idx:]
	}
	var output standardJSONOutput
	if err := json.Unmarshal(outputBytes, &output); err != nil {
		return "", "", fmt.Errorf("parse solc standard-json output: %w", err)
	}
	for _, item := range output.Errors {
		if item.Severity == "error" {
			return "", "", fmt.Errorf("solc Yul compilation error: %s", item.Message)
		}
	}
	for _, contractsByName := range output.Contracts {
		for _, contract := range contractsByName {
			if contract.EVM.Bytecode.Object == "" || contract.EVM.DeployedBytecode.Object == "" {
				continue
			}
			return "0x" + contract.EVM.Bytecode.Object, "0x" + contract.EVM.DeployedBytecode.Object, nil
		}
	}
	return "", "", fmt.Errorf("solc returned no Yul bytecode objects")
}

// deployContract executes the init code and returns the deployed contract address.
//
// Input:
// - cfg: runtime configuration containing state, sender, gas limit, and gas price
//
// Important detail:
//   - initCode is deployment-only code. It runs once during Create and returns the
//     runtime bytecode that will be stored in StateDB as the contract's code.
//
// Output:
// - the newly created contract address
func deployContract(cfg *runtime.Config, initBytecodeHex string) (common.Address, error) {
	initCode := common.FromHex(initBytecodeHex)
	_, addr, _, err := runtime.Create(initCode, cfg)
	return addr, err
}

// traceCall executes one scenario and converts Geth's raw trace into the UI model.
//
// Inputs:
// - id/name/description: scenario metadata for the viewer
// - addr: deployed contract address to call
// - input: calldata bytes passed into runtime.Call
// - baseCfg: shared runtime config template
// - manual: hand-computed gas breakdown for this path
// - statedb: shared in-memory state, so storage writes persist across scenarios
//
// Outputs:
// - scenarioData containing:
//   - inputHex: hex-encoded calldata
//   - returnHex: hex-encoded return bytes
//   - actualGasUsed: gas measured by Geth
//   - manualGasUsed: hand-summed gas estimate
//   - gasDelta: actual - manual
//   - steps: per-opcode snapshots for the viewer
//   - finalStorage: slot values after the call completes
func traceCall(id, name, description string, addr common.Address, input []byte, baseCfg *runtime.Config, manual []gasBreakdown, statedb *state.StateDB) (scenarioData, error) {
	structLogger := logger.NewStructLogger(&logger.Config{
		EnableMemory:   true,
		DisableStorage: false,
		DisableStack:   false,
	})

	cfg := *baseCfg
	cfg.State = statedb
	cfg.EVMConfig = vm.Config{Tracer: structLogger.Hooks()}

	ret, _, err := runtime.Call(addr, input, &cfg)
	if err != nil {
		return scenarioData{}, err
	}

	raw, err := structLogger.GetResult()
	if err != nil {
		return scenarioData{}, err
	}
	var trace traceResult
	if err := json.Unmarshal(raw, &trace); err != nil {
		return scenarioData{}, err
	}
	steps, err := decodeSteps(trace.StructLogs, cfg.GasLimit)
	if err != nil {
		return scenarioData{}, err
	}
	slot0 := common.Hash{}
	return scenarioData{
		ID:            id,
		Name:          name,
		Description:   description,
		InputHex:      hexutil.Encode(input),
		ReturnHex:     hexutil.Encode(ret),
		Failed:        trace.Failed,
		GasLimit:      cfg.GasLimit,
		ActualGasUsed: trace.Gas,
		ManualGasUsed: sumBreakdown(manual),
		GasDelta:      int64(trace.Gas) - int64(sumBreakdown(manual)),
		GasBreakdown:  manual,
		Steps:         steps,
		FinalStorage: map[string]string{
			slot0.Hex(): statedb.GetState(addr, slot0).Hex(),
		},
	}, nil
}

// decodeSteps converts the legacy StructLogger JSON entries into stepData.
//
// Inputs:
// - logs: raw per-opcode JSON objects emitted by StructLogger
// - gasLimit: used to derive "gas used before this step"
//
// Output:
// - a slice of normalized stepData entries that the HTML can render directly
//
// Notes:
//   - stack is reversed so index 0 is the top of stack in the UI
//   - memory is preserved as 32-byte chunks because the legacy trace format
//     already emits memory that way
func decodeSteps(logs []json.RawMessage, gasLimit uint64) ([]stepData, error) {
	steps := make([]stepData, 0, len(logs))
	for i, raw := range logs {
		var legacy legacyStep
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return nil, err
		}
		step := stepData{
			Index:    i,
			PC:       legacy.Pc,
			Op:       legacy.Op,
			Gas:      legacy.Gas,
			GasCost:  legacy.GasCost,
			Depth:    legacy.Depth,
			Error:    legacy.Error,
			Storage:  map[string]string{},
			GasUsed:  gasLimit - legacy.Gas,
			GasAfter: legacy.Gas - legacy.GasCost,
		}
		if legacy.Stack != nil {
			step.Stack = reverseCopy(*legacy.Stack)
		}
		if legacy.Memory != nil {
			step.Memory = append(step.Memory, *legacy.Memory...)
		}
		if legacy.Storage != nil {
			keys := make([]string, 0, len(*legacy.Storage))
			for key := range *legacy.Storage {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				step.Storage["0x"+strings.TrimPrefix(key, "0x")] = "0x" + strings.TrimPrefix((*legacy.Storage)[key], "0x")
			}
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// manualReadColdSload returns the hand-computed gas breakdown for the read path.
//
// Input:
// - none; the path is fixed by empty calldata
//
// Output:
// - ordered opcode costs with running totals, used for gas comparison in the UI
func manualReadColdSload() []gasBreakdown {
	return runningTotals([]gasBreakdown{
		{Op: "CALLDATASIZE", Cost: 2, Note: "VeryLow"},
		{Op: "PUSH1", Cost: 3, Note: "Push write-branch destination"},
		{Op: "JUMPI", Cost: 10, Note: "Condition is false but opcode still costs 10"},
		{Op: "PUSH1", Cost: 3, Note: "Push slot 0"},
		{Op: "SLOAD", Cost: 2100, Note: "Cold slot access under Berlin/London rules"},
		{Op: "PUSH1", Cost: 3, Note: "Memory offset 0"},
		{Op: "MSTORE", Cost: 6, Note: "3 base + 3 memory expansion to one 32-byte word"},
		{Op: "PUSH1", Cost: 3, Note: "Return size"},
		{Op: "PUSH1", Cost: 3, Note: "Return offset"},
		{Op: "RETURN", Cost: 0, Note: "No extra memory expansion"},
	})
}

// manualZeroToNonZeroSstore returns the hand-computed gas breakdown for the write
// path where slot 0 changes from zero to non-zero.
func manualZeroToNonZeroSstore() []gasBreakdown {
	return runningTotals([]gasBreakdown{
		{Op: "CALLDATASIZE", Cost: 2, Note: "VeryLow"},
		{Op: "PUSH1", Cost: 3, Note: "Push write-branch destination"},
		{Op: "JUMPI", Cost: 10, Note: "Branch into the write path"},
		{Op: "JUMPDEST", Cost: 1, Note: "Mark branch target"},
		{Op: "PUSH1", Cost: 3, Note: "Slot 0"},
		{Op: "CALLDATALOAD", Cost: 3, Note: "Read the 32-byte argument"},
		{Op: "PUSH1", Cost: 3, Note: "Push slot 0 for SSTORE"},
		{Op: "SSTORE", Cost: 22100, Note: "Cold zero->nonzero write: 2100 cold access + 20000 set cost"},
		{Op: "PUSH1", Cost: 3, Note: "Slot 0"},
		{Op: "SLOAD", Cost: 100, Note: "Warm read of the same slot after SSTORE"},
		{Op: "PUSH1", Cost: 3, Note: "Memory offset 0"},
		{Op: "MSTORE", Cost: 6, Note: "3 base + 3 memory expansion to one 32-byte word"},
		{Op: "PUSH1", Cost: 3, Note: "Return size"},
		{Op: "PUSH1", Cost: 3, Note: "Return offset"},
		{Op: "RETURN", Cost: 0, Note: "No extra memory expansion"},
	})
}

// runningTotals annotates each gas breakdown row with its cumulative total.
func runningTotals(in []gasBreakdown) []gasBreakdown {
	total := uint64(0)
	out := make([]gasBreakdown, len(in))
	for i, item := range in {
		total += item.Cost
		item.Total = total
		out[i] = item
	}
	return out
}

// sumBreakdown returns the final cumulative gas total for one manual breakdown.
func sumBreakdown(in []gasBreakdown) uint64 {
	if len(in) == 0 {
		return 0
	}
	return in[len(in)-1].Total
}

// reverseCopy flips stack order so the UI shows the top-of-stack first.
func reverseCopy(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i := range items {
		out[len(items)-1-i] = items[i]
	}
	return out
}

// leftPad32 converts a short byte slice into a 32-byte calldata word.
//
// Input:
// - a value such as []byte{0x2a}
//
// Output:
// - a full 32-byte big-endian word suitable for CALLDATALOAD-based contracts
func leftPad32(in []byte) []byte {
	out := make([]byte, 32)
	copy(out[32-len(in):], in)
	return out
}

// writeJSON writes the structured execution payload to disk in readable form.
//
// Output file:
// - trace.json
//
// The JSON is meant for inspection, debugging, and future non-HTML frontends.
func writeJSON(path string, data *visualizerData) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// writeHTML writes a standalone viewer page.
//
// Input:
// - the same visualizerData used for trace.json
//
// Output file:
// - visualizer.html
//
// Implementation detail:
//   - the JSON payload is base64-encoded and embedded into the HTML template so the
//     page can be opened directly without fetching external files.
func writeHTML(path string, data *visualizerData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	payload := base64.StdEncoding.EncodeToString(raw)
	tpl, err := template.New("visualizer").Parse(htmlTemplate)
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := tpl.Execute(&buf, map[string]string{"Payload": payload}); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

const htmlTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>EVM Execution Visualizer</title>
  <style>
    :root { color-scheme: dark; }
    body { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; margin: 0; background: #0f1115; color: #e6edf3; }
    header { padding: 16px 20px; border-bottom: 1px solid #30363d; }
    h1, h2, h3 { margin: 0 0 10px 0; font-weight: 600; }
    main { display: grid; grid-template-columns: 320px 1fr; min-height: calc(100vh - 68px); }
    aside { border-right: 1px solid #30363d; padding: 16px; overflow: auto; background: #151922; }
    section { padding: 16px 20px; overflow: auto; }
    select, button, input[type="range"] { width: 100%; margin: 6px 0 12px; }
    button { padding: 8px; background: #21262d; color: #e6edf3; border: 1px solid #30363d; cursor: pointer; }
    button:hover { background: #30363d; }
    pre, .box { background: #161b22; border: 1px solid #30363d; border-radius: 6px; padding: 12px; overflow: auto; }
    .kv { display: grid; grid-template-columns: 170px 1fr; gap: 6px 10px; margin-bottom: 14px; }
    .muted { color: #8b949e; }
    .grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
    table { width: 100%; border-collapse: collapse; }
    th, td { border-bottom: 1px solid #30363d; padding: 6px 8px; text-align: left; vertical-align: top; }
    .pill { display: inline-block; padding: 2px 8px; border: 1px solid #30363d; border-radius: 999px; }
    .warning { color: #ffa657; }
    .good { color: #3fb950; }
  </style>
</head>
<body>
  <header>
    <h1>EVM Execution Visualizer</h1>
    <div class="muted">Pure Yul contract, traced with go-ethereum StructLogger, rendered as a step-by-step HTML viewer.</div>
  </header>
  <main>
    <aside>
      <h2>Scenario</h2>
      <select id="scenario"></select>
      <div class="box">
        <div id="scenario-desc" class="muted"></div>
      </div>
      <h2>Step</h2>
      <button id="prev">Previous</button>
      <button id="next">Next</button>
      <input id="stepRange" type="range" min="0" max="0" value="0">
      <div class="box">
        <div id="step-label"></div>
      </div>
      <h2>Contract</h2>
      <div class="box">
        <div><span class="muted">Address</span><br><span id="contract-address"></span></div>
        <div style="margin-top:10px"><span class="muted">Runtime bytecode</span><br><span id="runtime-bytecode"></span></div>
      </div>
      <h2>Manual Gas Notes</h2>
      <div id="manual-notes"></div>
    </aside>
    <section>
      <div class="grid">
        <div>
          <h2>Scenario Summary</h2>
          <div class="kv box" id="summary"></div>
          <h2>Gas Breakdown</h2>
          <table id="gas-breakdown"></table>
        </div>
        <div>
          <h2>Current Step</h2>
          <div class="kv box" id="step-summary"></div>
          <h2>Stack</h2>
          <pre id="stack"></pre>
        </div>
      </div>
      <div class="grid" style="margin-top:16px">
        <div>
          <h2>Memory</h2>
          <pre id="memory"></pre>
        </div>
        <div>
          <h2>Storage Snapshot</h2>
          <pre id="storage"></pre>
        </div>
      </div>
      <div style="margin-top:16px">
        <h2>Yul Source</h2>
        <pre id="yul-source"></pre>
      </div>
    </section>
  </main>
  <script>
    const payload = JSON.parse(atob("{{ .Payload }}"));
    const els = {
      scenario: document.getElementById('scenario'),
      scenarioDesc: document.getElementById('scenario-desc'),
      contractAddress: document.getElementById('contract-address'),
      runtimeBytecode: document.getElementById('runtime-bytecode'),
      manualNotes: document.getElementById('manual-notes'),
      stepRange: document.getElementById('stepRange'),
      stepLabel: document.getElementById('step-label'),
      summary: document.getElementById('summary'),
      stepSummary: document.getElementById('step-summary'),
      gasBreakdown: document.getElementById('gas-breakdown'),
      stack: document.getElementById('stack'),
      memory: document.getElementById('memory'),
      storage: document.getElementById('storage'),
      yulSource: document.getElementById('yul-source'),
      prev: document.getElementById('prev'),
      next: document.getElementById('next'),
    };
    let scenarioIndex = 0;
    let stepIndex = 0;

    function setup() {
      payload.scenarios.forEach((item, idx) => {
        const opt = document.createElement('option');
        opt.value = String(idx);
        opt.textContent = item.name;
        els.scenario.appendChild(opt);
      });
      els.contractAddress.textContent = payload.contractAddress;
      els.runtimeBytecode.textContent = payload.contract.runtimeBytecode;
      els.yulSource.textContent = payload.contract.yulSource;
      payload.manualSummary.forEach((note) => {
        const box = document.createElement('div');
        box.className = 'box';
        box.style.marginBottom = '12px';
        box.innerHTML = '<strong>' + note.title + '</strong><div class="muted" style="margin-top:6px">' + note.body + '</div>';
        els.manualNotes.appendChild(box);
      });
      els.scenario.addEventListener('change', () => {
        scenarioIndex = Number(els.scenario.value);
        stepIndex = 0;
        render();
      });
      els.stepRange.addEventListener('input', () => {
        stepIndex = Number(els.stepRange.value);
        renderStep();
      });
      els.prev.addEventListener('click', () => {
        stepIndex = Math.max(0, stepIndex - 1);
        renderStep();
      });
      els.next.addEventListener('click', () => {
        const max = payload.scenarios[scenarioIndex].steps.length - 1;
        stepIndex = Math.min(max, stepIndex + 1);
        renderStep();
      });
      render();
    }

    function render() {
      const scenario = payload.scenarios[scenarioIndex];
      els.scenario.value = String(scenarioIndex);
      els.scenarioDesc.textContent = scenario.description;
      els.stepRange.max = String(Math.max(scenario.steps.length - 1, 0));
      els.stepRange.value = String(stepIndex);
      els.summary.innerHTML = kv([
        ['Input', scenario.inputHex],
        ['Return', scenario.returnHex],
        ['Actual gas used', String(scenario.actualGasUsed)],
        ['Manual gas used', String(scenario.manualGasUsed)],
        ['Delta', String(scenario.gasDelta)],
        ['Failed', String(scenario.failed)],
        ['Final slot0', Object.entries(scenario.finalStorage).map(([k,v]) => k + ' = ' + v).join('\n')],
      ]);
      renderGasBreakdown(scenario);
      renderStep();
    }

    function renderStep() {
      const scenario = payload.scenarios[scenarioIndex];
      const step = scenario.steps[stepIndex] || null;
      els.stepRange.value = String(stepIndex);
      els.stepLabel.textContent = step ? ('Step ' + step.index + ' / ' + (scenario.steps.length - 1)) : 'No steps';
      if (!step) {
        els.stepSummary.innerHTML = '';
        els.stack.textContent = '';
        els.memory.textContent = '';
        els.storage.textContent = '';
        return;
      }
      els.stepSummary.innerHTML = kv([
        ['PC', String(step.pc)],
        ['Opcode', step.op],
        ['Gas before', String(step.gas)],
        ['Gas cost', String(step.gasCost)],
        ['Gas after', String(step.gasAfter)],
        ['Gas used before step', String(step.gasUsedBefore)],
        ['Depth', String(step.depth)],
        ['Error', step.error || ''],
      ]);
      els.stack.textContent = step.stack.length ? step.stack.map((v, i) => '[' + i + '] ' + v).join('\n') : '(empty)';
      els.memory.textContent = step.memory.length ? step.memory.map((v, i) => '0x' + (i * 32).toString(16).padStart(4, '0') + ': ' + v).join('\n') : '(empty)';
      const storageEntries = Object.entries(step.storage || {});
      els.storage.textContent = storageEntries.length ? storageEntries.map(([k, v]) => k + ' => ' + v).join('\n') : '(no storage snapshot on this step)';
    }

    function renderGasBreakdown(scenario) {
      let html = '<tr><th>Opcode</th><th>Cost</th><th>Running</th><th>Note</th></tr>';
      scenario.gasBreakdown.forEach((row) => {
        html += '<tr><td>' + row.op + '</td><td>' + row.cost + '</td><td>' + row.runningTotal + '</td><td>' + row.note + '</td></tr>';
      });
      els.gasBreakdown.innerHTML = html;
    }

    function kv(rows) {
      return rows.map(([k, v]) => '<div class="muted">' + k + '</div><div>' + String(v).replace(/\n/g, '<br>') + '</div>').join('');
    }

    setup();
  </script>
</body>
</html>`
