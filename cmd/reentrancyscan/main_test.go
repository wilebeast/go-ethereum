package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeFlagsVulnerablePattern(t *testing.T) {
	code := mustDecodeHex("600054506000600060006000600060006000f1600060005500")
	insts, pcToIndex := disassemble(code)
	findings := analyze(insts, pcToIndex)
	if len(findings) == 0 {
		t.Fatalf("expected at least one finding")
	}
	if findings[0].SlotHex != "0x" {
		t.Fatalf("unexpected slot: %s", findings[0].SlotHex)
	}
}

func TestAnalyzeDoesNotFlagEffectBeforeInteraction(t *testing.T) {
	code := mustDecodeHex("6000545060006000556000600060006000600060006000f100")
	insts, pcToIndex := disassemble(code)
	findings := analyze(insts, pcToIndex)
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(findings))
	}
}

func TestAnalyzeTracksSimpleJump(t *testing.T) {
	// PUSH1 0x03 JUMP JUMPDEST PUSH1 0x00 SLOAD POP 7xPUSH0 CALL STOP
	code := mustDecodeHex("6003565b600054505f5f5f5f5f5f5ff100")
	insts, pcToIndex := disassemble(code)
	findings := analyze(insts, pcToIndex)
	if len(findings) == 0 {
		t.Fatalf("expected finding across jump path")
	}
}

func TestAnalyzeTracksMappingStyleSlot(t *testing.T) {
	// Simplified mapping-style path:
	// CALLER AND(mask) -> mstore(0)
	// push slot 0 -> mstore(32)
	// keccak256(0, 64) -> SLOAD
	// ...
	// CALL
	// ...
	// rebuild same slot -> SSTORE
	code := mustDecodeHex(
		"335f525f60205260405f205450" + // build slot and SLOAD, POP
			"5f5f5f5f5f5f5ff1" + // 7xPUSH0 CALL
			"335f525f60205260405f205f9055" + // rebuild slot and SSTORE(slot, 0)
			"00",
	)
	insts, pcToIndex := disassemble(code)
	findings := analyze(insts, pcToIndex)
	if len(findings) == 0 {
		t.Fatalf("expected finding for mapping-style slot")
	}
}

func TestAnalyzeFlagsHelperJumpPattern(t *testing.T) {
	// SLOAD
	// push return address + helper target
	// JUMP into helper
	// helper immediately JUMPs back using the saved return address
	// CALL
	// SSTORE after the CALL
	code := mustDecodeHex("6000545060096016565b5f5f5f5f5f5f5ff15f5f55005b56")
	insts, pcToIndex := disassemble(code)
	findings := analyze(insts, pcToIndex)
	if len(findings) == 0 {
		t.Fatalf("expected finding across helper-style jump/return path")
	}
}

func mustDecodeHex(s string) []byte {
	b, err := loadBytecode(s, "", "", "", "latest")
	if err != nil {
		panic(err)
	}
	return b
}

func TestAnalyzeTraceFlagsReentryPattern(t *testing.T) {
	logs := []traceLog{
		{Pc: 0x10, Depth: 1, OpName: "SLOAD"},
		{Pc: 0x20, Depth: 1, OpName: "CALL"},
		{Pc: 0x01, Depth: 2, OpName: "CALL"},
		{Pc: 0x10, Depth: 3, OpName: "SLOAD"},
	}
	findings := analyzeTrace(logs)
	if len(findings) == 0 {
		t.Fatalf("expected dynamic finding")
	}
}

func TestLoadTraceLogsSupportsResultWrapper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")
	payload := `{"result":{"structLogs":[{"pc":16,"depth":1,"op":"SLOAD"}]}}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write trace file: %v", err)
	}
	logs, err := loadTraceLogs(path)
	if err != nil {
		t.Fatalf("loadTraceLogs failed: %v", err)
	}
	if len(logs) != 1 || logs[0].Pc != 16 {
		t.Fatalf("unexpected trace logs: %+v", logs)
	}
}
