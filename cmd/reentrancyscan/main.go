package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/core/vm"
)

type instruction struct {
	pc        int
	op        vm.OpCode
	immediate []byte
	size      int
}

type absValue struct {
	value []byte
	expr  string
}

func (v absValue) known() bool {
	return v.expr != "" || v.value != nil
}

type finding struct {
	LoadPC   int
	CallPC   int
	SlotHex  string
	CallOp   vm.OpCode
	Severity string
}

type traceFinding struct {
	LoadPC       uint64
	CallPC       uint64
	ReentryPC    uint64
	OriginDepth  int
	ReentryDepth int
	Severity     string
}

type execState struct {
	index       int
	stack       []absValue
	loadedSlots map[string]int
	memory      map[uint64]absValue
}

type rpcResponse struct {
	Result json.RawMessage  `json:"result"`
	Error  *json.RawMessage `json:"error"`
	ID     json.RawMessage  `json:"id"`
}

type traceEnvelope struct {
	StructLogs []traceLog `json:"structLogs"`
}

type traceLog struct {
	Pc     uint64          `json:"pc"`
	Depth  int             `json:"depth"`
	Op     json.RawMessage `json:"op"`
	OpName string          `json:"opName"`
}

func main() {
	var (
		bytecodeHex = flag.String("bytecode", "", "runtime bytecode hex string")
		file        = flag.String("file", "", "file containing runtime bytecode")
		rpcURL      = flag.String("rpc", "", "JSON-RPC endpoint used with --address")
		address     = flag.String("address", "", "contract address used with --rpc to fetch bytecode via eth_getCode")
		blockTag    = flag.String("block", "latest", "block tag for eth_getCode")
		traceJSON   = flag.String("trace-json", "", "debug_traceTransaction JSON result file to analyze")
		disasm      = flag.Bool("disasm", false, "print disassembly before static findings")
	)
	flag.Parse()

	switch {
	case *traceJSON != "":
		logs, err := loadTraceLogs(*traceJSON)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		findings := analyzeTrace(logs)
		if len(findings) == 0 {
			fmt.Println("no dynamic reentrancy-trace findings")
			return
		}
		for _, f := range findings {
			fmt.Printf("[%s] depth=%d->%d sload_pc=0x%x call_pc=0x%x reentry_pc=0x%x\n", f.Severity, f.OriginDepth, f.ReentryDepth, f.LoadPC, f.CallPC, f.ReentryPC)
		}
	default:
		code, err := loadBytecode(*bytecodeHex, *file, *rpcURL, *address, *blockTag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		insts, pcToIndex := disassemble(code)
		if *disasm {
			printDisassembly(insts)
		}
		findings := analyze(insts, pcToIndex)
		if len(findings) == 0 {
			fmt.Println("no reentrancy-pattern findings")
			return
		}
		for _, f := range findings {
			fmt.Printf("[%s] slot=%s sload_pc=0x%x call_pc=0x%x call_op=%s\n", f.Severity, f.SlotHex, f.LoadPC, f.CallPC, f.CallOp)
		}
	}
}

func loadBytecode(bytecodeHex, file, rpcURL, address, blockTag string) ([]byte, error) {
	if countNonEmpty(bytecodeHex, file, address) > 1 {
		return nil, errors.New("use only one of --bytecode, --file, or --address")
	}
	switch {
	case address != "":
		if rpcURL == "" {
			return nil, errors.New("--rpc is required with --address")
		}
		codeHex, err := rpcEthGetCode(rpcURL, address, blockTag)
		if err != nil {
			return nil, err
		}
		bytecodeHex = codeHex
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		bytecodeHex = string(raw)
	}
	bytecodeHex = strings.TrimSpace(bytecodeHex)
	bytecodeHex = strings.TrimPrefix(bytecodeHex, "0x")
	if bytecodeHex == "" {
		return nil, errors.New("missing bytecode")
	}
	return hex.DecodeString(bytecodeHex)
}

func disassemble(code []byte) ([]instruction, map[int]int) {
	insts := make([]instruction, 0, len(code))
	pcToIndex := make(map[int]int)
	for pc := 0; pc < len(code); {
		op := vm.OpCode(code[pc])
		size := 1
		var immediate []byte
		if op >= vm.PUSH1 && op <= vm.PUSH32 {
			n := int(op-vm.PUSH1) + 1
			end := pc + 1 + n
			if end > len(code) {
				end = len(code)
			}
			immediate = append([]byte(nil), code[pc+1:end]...)
			size += n
		}
		pcToIndex[pc] = len(insts)
		insts = append(insts, instruction{pc: pc, op: op, immediate: immediate, size: size})
		pc += size
	}
	return insts, pcToIndex
}

func printDisassembly(insts []instruction) {
	for _, inst := range insts {
		if len(inst.immediate) > 0 {
			fmt.Printf("0x%04x: %-12s 0x%x\n", inst.pc, inst.op, inst.immediate)
		} else {
			fmt.Printf("0x%04x: %s\n", inst.pc, inst.op)
		}
	}
}

func analyze(insts []instruction, pcToIndex map[int]int) []finding {
	worklist := []execState{{index: 0, stack: nil, loadedSlots: map[string]int{}, memory: map[uint64]absValue{}}}
	visited := make(map[string]bool)
	found := make(map[string]finding)

	for len(worklist) > 0 {
		state := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		if state.index < 0 || state.index >= len(insts) {
			continue
		}
		key := stateKey(state)
		if visited[key] {
			continue
		}
		visited[key] = true

		inst := insts[state.index]
		nextState := cloneState(state)
		nextState.index++

		jumpTarget, jumpCond, halt := executeInstruction(inst, &nextState, found)
		if halt {
			continue
		}

		switch inst.op {
		case vm.JUMP:
			if targetIndex, ok := resolveJump(jumpTarget, insts, pcToIndex); ok {
				nextState.index = targetIndex
				worklist = append(worklist, nextState)
			}
		case vm.JUMPI:
			if fallthroughAllowed(jumpCond) {
				worklist = append(worklist, nextState)
			}
			if targetIndex, ok := resolveJump(jumpTarget, insts, pcToIndex); ok && jumpTakenPossible(jumpCond) {
				jumpState := cloneState(nextState)
				jumpState.index = targetIndex
				worklist = append(worklist, jumpState)
			}
		default:
			worklist = append(worklist, nextState)
		}
	}

	results := make([]finding, 0, len(found))
	for _, finding := range found {
		results = append(results, finding)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].CallPC != results[j].CallPC {
			return results[i].CallPC < results[j].CallPC
		}
		return results[i].SlotHex < results[j].SlotHex
	})
	return results
}

func analyzeTrace(logs []traceLog) []traceFinding {
	type frameState struct {
		pendingSloadPC uint64
		hasPendingLoad bool
		pendingCallPC  uint64
		pendingCallOp  string
	}
	frames := make(map[int]*frameState)
	found := make(map[string]traceFinding)

	getFrame := func(depth int) *frameState {
		f, ok := frames[depth]
		if !ok {
			f = &frameState{}
			frames[depth] = f
		}
		return f
	}

	for _, log := range logs {
		op := log.OpcodeString()
		if op == "" {
			continue
		}
		frame := getFrame(log.Depth)
		switch op {
		case "SLOAD":
			frame.pendingSloadPC = log.Pc
			frame.hasPendingLoad = true
		case "SSTORE":
			frame.hasPendingLoad = false
			frame.pendingCallPC = 0
			frame.pendingCallOp = ""
		case "CALL", "CALLCODE", "DELEGATECALL":
			if frame.hasPendingLoad {
				frame.pendingCallPC = log.Pc
				frame.pendingCallOp = op
			}
		}

		for depth, parent := range frames {
			if depth >= log.Depth {
				continue
			}
			if !parent.hasPendingLoad || parent.pendingCallPC == 0 {
				continue
			}
			if op == "SLOAD" || op == "CALL" || op == "CALLCODE" || op == "DELEGATECALL" {
				key := fmt.Sprintf("%d/%d/%d", depth, parent.pendingCallPC, log.Pc)
				found[key] = traceFinding{
					LoadPC:       parent.pendingSloadPC,
					CallPC:       parent.pendingCallPC,
					ReentryPC:    log.Pc,
					OriginDepth:  depth,
					ReentryDepth: log.Depth,
					Severity:     "warning",
				}
			}
		}
	}

	results := make([]traceFinding, 0, len(found))
	for _, finding := range found {
		results = append(results, finding)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].CallPC != results[j].CallPC {
			return results[i].CallPC < results[j].CallPC
		}
		if results[i].ReentryPC != results[j].ReentryPC {
			return results[i].ReentryPC < results[j].ReentryPC
		}
		return results[i].OriginDepth < results[j].OriginDepth
	})
	return results
}

func executeInstruction(inst instruction, state *execState, found map[string]finding) (jumpTarget absValue, jumpCond absValue, halt bool) {
	switch {
	case inst.op == vm.PUSH0:
		push(state, absConst([]byte{0}))
	case inst.op >= vm.PUSH1 && inst.op <= vm.PUSH32:
		push(state, absConst(inst.immediate))
	case inst.op >= vm.DUP1 && inst.op <= vm.DUP16:
		n := int(inst.op-vm.DUP1) + 1
		push(state, peek(state, n))
	case inst.op >= vm.SWAP1 && inst.op <= vm.SWAP16:
		n := int(inst.op-vm.SWAP1) + 2
		swap(state, n)
	case inst.op == vm.POP:
		pop(state)
	case inst.op == vm.SLOAD:
		slot := normalizeSlot(pop(state))
		if slot != "" {
			state.loadedSlots[slot] = inst.pc
		}
		push(state, absUnknown())
	case inst.op == vm.SSTORE:
		slot := normalizeSlot(pop(state))
		pop(state)
		if slot != "" {
			delete(state.loadedSlots, slot)
		}
	case inst.op == vm.JUMP:
		jumpTarget = pop(state)
	case inst.op == vm.JUMPI:
		jumpTarget = pop(state)
		jumpCond = pop(state)
	case inst.op == vm.MSTORE:
		offset := pop(state)
		value := pop(state)
		if off, ok := asUint64(offset); ok {
			state.memory[off] = value
		} else {
			state.memory = map[uint64]absValue{}
		}
	case isCallLike(inst.op):
		popCallArguments(state, inst.op)
		for slot, loadPC := range state.loadedSlots {
			key := fmt.Sprintf("%d/%d/%s", loadPC, inst.pc, slot)
			found[key] = finding{
				LoadPC:   loadPC,
				CallPC:   inst.pc,
				SlotHex:  slot,
				CallOp:   inst.op,
				Severity: "warning",
			}
		}
		push(state, absUnknown())
	case inst.op == vm.STOP || inst.op == vm.RETURN || inst.op == vm.REVERT || inst.op == vm.INVALID || inst.op == vm.SELFDESTRUCT:
		return absValue{}, absValue{}, true
	default:
		applyGenericStackEffect(state, inst.op)
	}
	return jumpTarget, jumpCond, false
}

func isCallLike(op vm.OpCode) bool {
	return op == vm.CALL || op == vm.CALLCODE || op == vm.DELEGATECALL
}

func popCallArguments(state *execState, op vm.OpCode) {
	switch op {
	case vm.CALL, vm.CALLCODE:
		for range 7 {
			pop(state)
		}
	case vm.DELEGATECALL:
		for range 6 {
			pop(state)
		}
	}
}

func applyGenericStackEffect(state *execState, op vm.OpCode) {
	switch op {
	case vm.ADD, vm.SUB, vm.MUL, vm.DIV, vm.SDIV, vm.MOD, vm.SMOD, vm.EXP, vm.SIGNEXTEND,
		vm.LT, vm.GT, vm.SLT, vm.SGT, vm.EQ, vm.AND, vm.OR, vm.XOR, vm.BYTE, vm.SHL, vm.SHR, vm.SAR:
		x := pop(state)
		y := pop(state)
		push(state, combineBinary(op, x, y))
	case vm.ISZERO, vm.NOT:
		x := pop(state)
		push(state, combineUnary(op, x))
	case vm.KECCAK256:
		offset := pop(state)
		size := pop(state)
		push(state, combineKeccak(state, offset, size))
	case vm.MLOAD:
		offset := pop(state)
		if off, ok := asUint64(offset); ok {
			push(state, state.memory[off])
		} else {
			push(state, absUnknown())
		}
	case vm.MSTORE8:
		pop(state)
		pop(state)
		state.memory = map[uint64]absValue{}
	case vm.CALLER:
		push(state, absCaller())
	case vm.CALLDATALOAD, vm.CODESIZE, vm.CALLDATASIZE, vm.CALLVALUE, vm.ADDRESS, vm.ORIGIN, vm.GAS, vm.MSIZE, vm.RETURNDATASIZE, vm.EXTCODESIZE, vm.EXTCODEHASH, vm.BALANCE, vm.SELFBALANCE:
		if op == vm.EXTCODESIZE || op == vm.EXTCODEHASH || op == vm.BALANCE {
			pop(state)
		}
		push(state, absUnknown())
	case vm.CALLDATACOPY, vm.CODECOPY, vm.RETURNDATACOPY, vm.EXTCODECOPY:
		pop(state)
		pop(state)
		pop(state)
		if op == vm.EXTCODECOPY {
			pop(state)
		}
		state.memory = map[uint64]absValue{}
	case vm.LOG0, vm.LOG1, vm.LOG2, vm.LOG3, vm.LOG4:
		topics := int(op - vm.LOG0)
		pop(state)
		pop(state)
		for range topics {
			pop(state)
		}
	case vm.CREATE:
		pop(state)
		pop(state)
		pop(state)
		push(state, absUnknown())
	case vm.CREATE2:
		pop(state)
		pop(state)
		pop(state)
		pop(state)
		push(state, absUnknown())
	default:
		state.stack = nil
	}
}

func combineBinary(op vm.OpCode, x, y absValue) absValue {
	switch op {
	case vm.AND:
		if x.expr == "CALLER" && isAllOnes(y.value) {
			return x
		}
		if y.expr == "CALLER" && isAllOnes(x.value) {
			return y
		}
		if xv, ok := asUint64(x); ok {
			if yv, ok := asUint64(y); ok {
				return absUint64(xv & yv)
			}
		}
		return absUnknown()
	case vm.ADD:
		xv, xok := asUint64(x)
		yv, yok := asUint64(y)
		if !xok || !yok {
			return absUnknown()
		}
		return absUint64(xv + yv)
	case vm.SUB:
		xv, xok := asUint64(x)
		yv, yok := asUint64(y)
		if !xok || !yok {
			return absUnknown()
		}
		return absUint64(xv - yv)
	case vm.EQ:
		xv, xok := asUint64(x)
		yv, yok := asUint64(y)
		if !xok || !yok {
			return absUnknown()
		}
		if xv == yv {
			return absUint64(1)
		}
		return absUint64(0)
	case vm.SHL:
		xv, xok := asUint64(x)
		yv, yok := asUint64(y)
		if !xok || !yok {
			return absUnknown()
		}
		return absUint64(yv << xv)
	case vm.SHR:
		xv, xok := asUint64(x)
		yv, yok := asUint64(y)
		if !xok || !yok {
			return absUnknown()
		}
		return absUint64(yv >> xv)
	default:
		return absUnknown()
	}
}

func combineUnary(op vm.OpCode, x absValue) absValue {
	xv, ok := asUint64(x)
	if !ok {
		return absUnknown()
	}
	switch op {
	case vm.ISZERO:
		if xv == 0 {
			return absUint64(1)
		}
		return absUint64(0)
	default:
		return absUnknown()
	}
}

func resolveJump(v absValue, insts []instruction, pcToIndex map[int]int) (int, bool) {
	if !v.known() || len(v.value) > 8 {
		return 0, false
	}
	pc := int(bytesToUint64(v.value))
	idx, ok := pcToIndex[pc]
	if !ok || insts[idx].op != vm.JUMPDEST {
		return 0, false
	}
	return idx, true
}

func fallthroughAllowed(cond absValue) bool {
	return !cond.known() || bytesToUint64(cond.value) == 0
}

func jumpTakenPossible(cond absValue) bool {
	return !cond.known() || bytesToUint64(cond.value) != 0
}

func stateKey(state execState) string {
	stackParts := make([]string, 0, min(len(state.stack), 8))
	for i := max(0, len(state.stack)-8); i < len(state.stack); i++ {
		stackParts = append(stackParts, describe(state.stack[i]))
	}
	slotKeys := make([]string, 0, len(state.loadedSlots))
	for slot, pc := range state.loadedSlots {
		slotKeys = append(slotKeys, fmt.Sprintf("%s@%d", slot, pc))
	}
	sort.Strings(slotKeys)
	memKeys := make([]string, 0, len(state.memory))
	for off, val := range state.memory {
		memKeys = append(memKeys, fmt.Sprintf("%d=%s", off, describe(val)))
	}
	sort.Strings(memKeys)
	return fmt.Sprintf("%d|%s|%s|%s", state.index, strings.Join(stackParts, ","), strings.Join(slotKeys, ","), strings.Join(memKeys, ","))
}

func cloneState(state execState) execState {
	stack := append([]absValue(nil), state.stack...)
	loaded := make(map[string]int, len(state.loadedSlots))
	for k, v := range state.loadedSlots {
		loaded[k] = v
	}
	memory := make(map[uint64]absValue, len(state.memory))
	for k, v := range state.memory {
		memory[k] = v
	}
	return execState{index: state.index, stack: stack, loadedSlots: loaded, memory: memory}
}

func absConst(b []byte) absValue {
	trimmed := trimLeadingZeros(b)
	if len(trimmed) == 0 {
		trimmed = []byte{0}
	}
	return absValue{value: append([]byte(nil), trimmed...)}
}

func absUint64(v uint64) absValue {
	if v == 0 {
		return absConst([]byte{0})
	}
	var out [8]byte
	for i := 7; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return absConst(out[:])
}

func absUnknown() absValue { return absValue{} }

func absCaller() absValue { return absValue{expr: "CALLER"} }

func absExpr(expr string) absValue { return absValue{expr: expr} }

func push(state *execState, v absValue) {
	state.stack = append(state.stack, v)
}

func pop(state *execState) absValue {
	if len(state.stack) == 0 {
		return absUnknown()
	}
	v := state.stack[len(state.stack)-1]
	state.stack = state.stack[:len(state.stack)-1]
	return v
}

func peek(state *execState, depth int) absValue {
	if depth <= 0 || len(state.stack) < depth {
		return absUnknown()
	}
	return state.stack[len(state.stack)-depth]
}

func swap(state *execState, depth int) {
	if depth <= 1 || len(state.stack) < depth {
		return
	}
	top := len(state.stack) - 1
	other := len(state.stack) - depth
	state.stack[top], state.stack[other] = state.stack[other], state.stack[top]
}

func normalizeSlot(v absValue) string {
	switch {
	case v.expr != "":
		return v.expr
	case v.value != nil:
		return "0x" + hex.EncodeToString(trimLeadingZeros(v.value))
	default:
		return ""
	}
}

func trimLeadingZeros(b []byte) []byte {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

func bytesToUint64(b []byte) uint64 {
	var out uint64
	for _, by := range b {
		out = (out << 8) | uint64(by)
	}
	return out
}

func asUint64(v absValue) (uint64, bool) {
	if v.value == nil || len(v.value) > 8 {
		return 0, false
	}
	return bytesToUint64(v.value), true
}

func describe(v absValue) string {
	switch {
	case v.expr != "":
		return v.expr
	case v.value != nil:
		return "0x" + hex.EncodeToString(v.value)
	default:
		return "?"
	}
}

func isAllOnes(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, by := range b {
		if by != 0xff {
			return false
		}
	}
	return true
}

func combineKeccak(state *execState, offset, size absValue) absValue {
	off, okOff := asUint64(offset)
	sz, okSz := asUint64(size)
	if !okOff || !okSz || sz != 64 {
		return absUnknown()
	}
	first, ok1 := state.memory[off]
	second, ok2 := state.memory[off+32]
	if !ok1 || !ok2 {
		return absUnknown()
	}
	return absExpr(fmt.Sprintf("keccak(%s,%s)", describe(first), describe(second)))
}

func countNonEmpty(values ...string) int {
	var n int
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	return n
}

func rpcEthGetCode(rpcURL, address, blockTag string) (string, error) {
	raw, err := rpcCall(rpcURL, "eth_getCode", []any{address, blockTag})
	if err != nil {
		return "", err
	}
	var code string
	if err := json.Unmarshal(raw, &code); err != nil {
		return "", err
	}
	return code, nil
}

func loadTraceLogs(path string) ([]traceLog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var env traceEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && len(env.StructLogs) > 0 {
		return env.StructLogs, nil
	}
	var wrapper struct {
		Result traceEnvelope `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Result.StructLogs) > 0 {
		return wrapper.Result.StructLogs, nil
	}
	return nil, errors.New("could not find structLogs in trace json")
}

func (l traceLog) OpcodeString() string {
	if l.OpName != "" {
		return l.OpName
	}
	if len(l.Op) == 0 {
		return ""
	}
	if l.Op[0] == '"' {
		var s string
		if json.Unmarshal(l.Op, &s) == nil {
			return s
		}
	}
	var num uint64
	if json.Unmarshal(l.Op, &num) == nil {
		return vm.OpCode(byte(num)).String()
	}
	var hexNum string
	if json.Unmarshal(l.Op, &hexNum) == nil {
		hexNum = strings.TrimPrefix(hexNum, "0x")
		if parsed, err := strconv.ParseUint(hexNum, 16, 8); err == nil {
			return vm.OpCode(byte(parsed)).String()
		}
	}
	return ""
}

func rpcCall(rpcURL, method string, params []any) (json.RawMessage, error) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, rpcURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out rpcResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s failed: %s", method, string(*out.Error))
	}
	return out.Result, nil
}
