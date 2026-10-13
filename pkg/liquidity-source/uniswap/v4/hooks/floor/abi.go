package floor

import (
	"bytes"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

var (
	hookABI      abi.ABI // SingletonFloorHook: poolState, execute
	poolViewsABI abi.ABI // the per-pool views reached through execute(poolId, data): stairs
	stateViewABI abi.ABI // Uniswap v4 StateView: getSlot0
)

func init() {
	for _, item := range []struct {
		dst  *abi.ABI
		json []byte
	}{
		{&hookABI, hookABIJson},
		{&poolViewsABI, poolViewsABIJson},
		{&stateViewABI, stateViewABIJson},
	} {
		parsed, err := abi.JSON(bytes.NewReader(item.json))
		if err != nil {
			panic(err)
		}
		*item.dst = parsed
	}
}
