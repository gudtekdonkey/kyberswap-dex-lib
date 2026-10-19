package flooramm_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	flooramm "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/floor-amm"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/msgpack"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool"
)

// The simulator survives the router's msgpack round trip and quotes the same after it (a fresh launch, and one whose lot
// holds bins).
func TestMsgpackRoundTrip(t *testing.T) {
	for _, mk := range []func(*testing.T) (*flooramm.PoolSimulator, *big.Int){flooramm.FreshSimForTest, flooramm.LotSimForTest} {
		roundTrip(t, mk)
	}
}

// roundTrip: one simulator through msgpack, the same quote after.
func roundTrip(t *testing.T, mk func(*testing.T) (*flooramm.PoolSimulator, *big.Int)) {
	sim, amt := mk(t)
	enc, err := msgpack.EncodePoolSimulatorsMap(map[string]pool.IPoolSimulator{"p": sim})
	require.NoError(t, err)
	dec, err := msgpack.DecodePoolSimulatorsMap(enc)
	require.NoError(t, err)
	params := pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: sim.GetTokens()[0], Amount: amt}, TokenOut: sim.GetTokens()[1]}
	a, err := sim.CalcAmountOut(params)
	require.NoError(t, err)
	b, err := dec["p"].CalcAmountOut(params)
	require.NoError(t, err)
	require.Equal(t, a.TokenAmountOut.Amount, b.TokenAmountOut.Amount)
}
