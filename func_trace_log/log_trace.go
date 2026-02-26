package func_trace_log

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"

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

	// Get caller information (skip 2 frames: this function and its caller)
	_, file, line, ok := runtime.Caller(2)
	if ok {
		// Extract just the filename with parent directory
		callerInfo := getCallerInfo(file, line)
		// Use custom log format to show the actual caller location instead of logging function location
		customLogInfo("Calling function", callerInfo, "callee", name, "arguments", string(argsBytes), "result", string(resultBytes))
	} else {
		log.Info("Calling function", "callee", name, "arguments", string(argsBytes), "result", string(resultBytes))
	}
}

// getCallerInfo extracts filename with parent directory and line number
func getCallerInfo(file string, line int) string {
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

	var callerInfo string
	if secondLastSlash != -1 {
		// Include parent directory: parent/filename
		callerInfo = file[secondLastSlash+1:]
	} else if lastSlash != -1 {
		// Just filename if no parent directory
		callerInfo = file[lastSlash+1:]
	} else {
		// Full path if no slashes found
		callerInfo = file
	}

	return callerInfo + ":" + strconv.Itoa(line)
}

// customLogInfo creates a log message with custom caller location
func customLogInfo(msg, callerInfo string, attrs ...interface{}) {
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
	log.Info(fmt.Sprintf("%s %s", callerInfo, msg) + logMsg)
}
