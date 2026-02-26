package ellen

import (
	"encoding/json"

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

	log.Info("Calling function", "callee", name, "arguments", string(argsBytes), "result", string(resultBytes))
}
