package flooramm

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"
)

const (
	DexType = "floor-amm"

	// Gas: the measured buy and sell on the v1.2 contract (seven-chain fork rehearsal), worst case with headroom.
	buyGas  = 475_000
	sellGas = 335_000

	defaultNewPoolLimit = 100
)

// deadAddress: tokens sent here count as burned in the program's live supply (totalSupply − balanceOf(dead)).
var deadAddress = common.HexToAddress("0x000000000000000000000000000000000000dEaD")

var (
	ErrInvalidToken    = errors.New("invalid token")
	ErrOverflow        = errors.New("overflow")
	ErrZeroOut         = errors.New("zero amount out")
	ErrInvalidSnapshot = errors.New("invalid launch snapshot")
)
