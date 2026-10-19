// Package floormath is a Go port of the protocol's reference trade path (Rust: fixed, schedule, bid, fees, preset, trade,
// supply, launch). It reproduces golden/launch.json receipt for receipt (launch_golden_test.go). The Rust crate is the
// source; this follows it, never the other way.
//
// Every Rust u128 is a uint256.Int value here, held under 2^128 wherever the Rust holds it there: a checked Rust op is a
// checked op here (Overflow past 2^128), a plain Rust op is a plain op (the goldens run in debug, so none of them wraps).
package floormath

import "github.com/holiman/uint256"

// U is one Rust u128 (or U256) value.
type U = uint256.Int

// MathError is floor_math::MathError; the string is the variant's name, as the goldens print it. APPEND-ONLY.
type MathError string

func (e MathError) Error() string { return string(e) }

const (
	ErrRatioOutOfRange      MathError = "RatioOutOfRange"
	ErrVaultShareOutOfRange MathError = "VaultShareOutOfRange"
	ErrZero                 MathError = "Zero"
	ErrOverflow             MathError = "Overflow"
	ErrRampOutOfRange       MathError = "RampOutOfRange"
	ErrUnknownPreset        MathError = "UnknownPreset"
	ErrPresetInvalid        MathError = "PresetInvalid"
	ErrNotOutside           MathError = "NotOutside"
	ErrLocked               MathError = "Locked"
	ErrNotDue               MathError = "NotDue"
)

// BPS is 10,000 bps = 100 %.
const BPS = 10_000

var (
	zero   U
	one    = n(1)
	one64  = lsh(n(1), 64) // ONE: 1.0 in Q64.64
	mask64 = sub(one64, one)
	max128 = sub(lsh(n(1), 128), one)
	bpsU   = n(BPS)
)

func n(v uint64) U { var z U; z.SetUint64(v); return z }

// D parses a decimal string (the goldens' u128s).
func D(s string) U { return *uint256.MustFromDecimal(s) }

func hx(s string) U { return *uint256.MustFromHex(s) }

func add(a, b U) U { var z U; z.Add(&a, &b); return z }

func sub(a, b U) U { var z U; z.Sub(&a, &b); return z }

// mul is the exact product of two values under 2^128 (U256::mul_u128).
func mul(a, b U) U { var z U; z.Mul(&a, &b); return z }

func div(a, b U) U { var z U; z.Div(&a, &b); return z }

func mod(a, b U) U { var z U; z.Mod(&a, &b); return z }

func lsh(a U, k uint) U { var z U; z.Lsh(&a, k); return z }

func rsh(a U, k uint) U { var z U; z.Rsh(&a, k); return z }

func and(a, b U) U { var z U; z.And(&a, &b); return z }

func or(a, b U) U { var z U; z.Or(&a, &b); return z }

func lt(a, b U) bool { return a.Lt(&b) }
func gt(a, b U) bool { return a.Gt(&b) }
func le(a, b U) bool { return !a.Gt(&b) }
func ge(a, b U) bool { return !a.Lt(&b) }
func eq(a, b U) bool { return a.Eq(&b) }
func isZ(a U) bool   { return a.IsZero() }

func umin(a, b U) U {
	if lt(a, b) {
		return a
	}
	return b
}

func umax(a, b U) U {
	if gt(a, b) {
		return a
	}
	return b
}

// lz is u128::leading_zeros.
func lz(a U) uint { return 128 - uint(a.BitLen()) }

func fits(a U) bool { return a.BitLen() <= 128 }

// toU128 is U256::to_u128: Overflow past 2^128.
func toU128(a U) (U, error) {
	if !fits(a) {
		return zero, ErrOverflow
	}
	return a, nil
}

// addC is u128::checked_add.
func addC(a, b U) (U, error) { return toU128(add(a, b)) }

// subC is u128::checked_sub.
func subC(a, b U) (U, error) {
	if lt(a, b) {
		return zero, ErrOverflow
	}
	return sub(a, b), nil
}

// mulC is u128::checked_mul.
func mulC(a, b U) (U, error) { return toU128(mul(a, b)) }

// satSub is u128::saturating_sub.
func satSub(a, b U) U {
	if lt(a, b) {
		return zero
	}
	return sub(a, b)
}

// satAdd is u128::saturating_add.
func satAdd(a, b U) U { return umin(add(a, b), max128) }

// divCeil is U256::div_ceil_u128 (d ≠ 0).
func divCeil(a, d U) U {
	q, r := div(a, d), mod(a, d)
	if isZ(r) {
		return q
	}
	return add(q, one)
}

// shl64C is U256::checked_shl64 of a value under 2^256: None past 2^256.
func shl64C(a U) (U, error) {
	if a.BitLen() > 192 {
		return zero, ErrOverflow
	}
	return lsh(a, 64), nil
}

// mulDivDown is ⌊a·b ÷ c⌋ (Zero for c = 0, Overflow past 2^128).
func mulDivDown(a, b, c U) (U, error) {
	if isZ(c) {
		return zero, ErrZero
	}
	return toU128(div(mul(a, b), c))
}

// mulDivUp is ⌈a·b ÷ c⌉.
func mulDivUp(a, b, c U) (U, error) {
	if isZ(c) {
		return zero, ErrZero
	}
	return toU128(divCeil(mul(a, b), c))
}

// isqrt is schedule::isqrt_u256: ⌊√n⌋ (Newton from any start at or above the root lands on the same bits).
func isqrt(a U) U {
	var z U
	z.Sqrt(&a)
	return z
}

// The few signed (i128) steps of the walk, as two's-complement Us (every value is under 2^127 in magnitude).

var minus1 = neg(one)

func neg(a U) U { var z U; z.Neg(&a); return z }

func slt(a, b U) bool { return a.Slt(&b) }

func sgt(a, b U) bool { return a.Sgt(&b) }

func sabs(a U) U {
	if a.Sign() < 0 {
		return neg(a)
	}
	return a
}

// sdiv2 is i128 / 2 (truncating toward zero).
func sdiv2(a U) U { var z U; t := n(2); z.SDiv(&a, &t); return z }

// u64 is the low 64 bits.
func u64(a U) uint64 { return a.Uint64() }
