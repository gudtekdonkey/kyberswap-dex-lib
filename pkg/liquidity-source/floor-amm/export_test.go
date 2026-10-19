package flooramm

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// FreshSimForTest is freshSim for the external msgpack test.
func FreshSimForTest(t *testing.T) (*PoolSimulator, *big.Int) { return freshSim(t, 1_200) }

// LotSimForTest is a simulator whose launch's lot holds bins, and one whole quote to buy with.
func LotSimForTest(t *testing.T) (*PoolSimulator, *big.Int) {
	ep, _, _, q := lotEntity(t)
	sim, err := NewPoolSimulator(ep)
	require.NoError(t, err)
	return sim, q.ToBig()
}
