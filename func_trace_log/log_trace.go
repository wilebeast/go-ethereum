package func_trace_log

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/ethereum/go-ethereum/log"
)

func Printf(name string, args map[string]interface{}, result map[string]interface{}) {
	argsBytes, _ := json.Marshal(args)
	resultBytes, _ := json.Marshal(result)
	// ctxArgs := args["ctx"]
	// if ctxArgs != nil {
	// if ctx, ok := ctxArgs.(context.Context); ok {
	// delete(args, "ctx")
	// logs.CtxInfo(ctx, "Calling %s with arguments: %s, result:%s\n", name, string(argsBytes), string(resultBytes))
	// }
	// } else {
	// logs.CtxInfo(context.Background(), "Calling %s with arguments: %s, result:%s\n", name, string(argsBytes), string(resultBytes))
	// }

	var callerInfo string
	var calleeInfo string
	// Get callee information (skip 2 frames: this function and its caller)
	calleePc, calleeFile, calleeLine, ok := runtime.Caller(2)
	if ok {
		calleeInfo = getFunctionInfo(calleePc, calleeFile, calleeLine)
	}

	// Get caller information (skip 3 frames: this function, its caller, and the callee)
	callerPc, callerFile, callerLine, ok := runtime.Caller(3)
	if ok {
		callerInfo = getFunctionInfo(callerPc, callerFile, callerLine)
	}
	// Use custom log format with both caller and callee information
	customLogInfo("("+callerInfo+")"+" -> "+"("+calleeInfo+")", "arguments", string(argsBytes), "result", string(resultBytes))
}

// getFileInfo extracts filename with parent directory
func getFileInfo(file string) string {
	// Extract filename with parent directory
	lastSlash := -1
	secondLastSlash := -1
	for i := len(file) - 1; i >= 0; i-- {
		if file[i] == '/' {
			if lastSlash == -1 {
				lastSlash = i
			} else {
				secondLastSlash = i
				break
			}
		}
	}

	var fileInfo string
	if secondLastSlash != -1 {
		// Include parent directory: parent/filename
		fileInfo = file[secondLastSlash+1:]
	} else if lastSlash != -1 {
		// Just filename if no parent directory
		fileInfo = file[lastSlash+1:]
	} else {
		// Full path if no slashes found
		fileInfo = file
	}

	return fileInfo
}

// getFunctionInfo extracts function information with shortened function name
func getFunctionInfo(pc uintptr, file string, line int) string {
	// Get the function name from the program counter
	if fn := runtime.FuncForPC(pc); fn != nil {
		// Extract just the last part of the function name (after last slash)
		fullName := fn.Name()
		lastSlash := -1
		for i := len(fullName) - 1; i >= 0; i-- {
			if fullName[i] == '/' {
				lastSlash = i
				break
			}
		}
		var shortName string
		if lastSlash != -1 {
			shortName = fullName[lastSlash+1:]
		} else {
			shortName = fullName
		}
		return fmt.Sprintf("%s:%d (%s)", getFileInfo(file), line, shortName)
	} else {
		return fmt.Sprintf("%s:%d", getFileInfo(file), line)
	}
}

// customLogInfo creates a log message with custom caller location
func customLogInfo(calleeInfo string, attrs ...interface{}) {
	// Create a custom log record with the desired caller information
	if len(attrs)%2 != 0 {
		// If odd number of arguments, log error and use standard logger
		log.Error("Invalid number of arguments for customLogInfo")
		return
	}

	// Format the log message manually to include caller info in the right position
	var logMsg string
	for i := 0; i < len(attrs); i += 2 {
		if key, ok := attrs[i].(string); ok {
			if value, ok := attrs[i+1].(string); ok {
				logMsg += fmt.Sprintf(" %s=%s", key, value)
			} else {
				logMsg += fmt.Sprintf(" %s=%v", key, attrs[i+1])
			}
		}
	}

	// Use the standard logger but with a custom message that includes caller info
	log.Info(fmt.Sprintf("%s", calleeInfo) + logMsg)
}
