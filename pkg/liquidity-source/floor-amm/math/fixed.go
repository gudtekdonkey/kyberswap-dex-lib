package floormath

// fixed.rs: log2_x64 / exp2_x64 in Q64.64, both truncating.

// kHex[i − 1] = ⌊2^(2^-i) · 2^127⌋ for i = 1..=64 (fixed.rs `K`, Q1.127).
var kHex = [64]string{
	"b504f333f9de6484597d89b3754abe9f",
	"9837f0518db8a96f46ad23182e42f6f6",
	"8b95c1e3ea8bd6e6fbe4628758a53c90",
	"85aac367cc487b14c5c95b8c2154c1b2",
	"82cd8698ac2ba1d73e2a475b46520bff",
	"8164d1f3bc0307737be56527bd14def4",
	"80b1ed4fd999ab6c25335719b6e6fd20",
	"8058d7d2d5e5f6b094d589f608ee4aa2",
	"802c6436d0e04f50ff8ce94a6797b3ce",
	"8016302f174676283690dfe44d11d008",
	"800b179c82028fd0945e54e2ae18f2f0",
	"80058baf7fee3b5d1c718b38e549cb93",
	"8002c5d00fdcfcb6b6566a58c048be1f",
	"800162e61bed4a48e84c2e1a463473d9",
	"8000b17292f702a3aa22beacca949013",
	"800058b92abbae02030c5fa5256f41fe",
	"80002c5c8dade4d71776c0f4dbea67d6",
	"8000162e44eaf636526be456600bdbe4",
	"80000b1721fa7c188307016c1cd4e8b6",
	"8000058b90de7e4cecfc487503488bb1",
	"800002c5c8678f36cbfce50a6de60b14",
	"80000162e431db9f80b2347b5d62e516",
	"800000b1721872d0c7b08cf1e0114152",
	"80000058b90c1aa8a5c3736cb77e8dff",
	"8000002c5c8605a4635f2efc2362d978",
	"800000162e4300e635cf4a109e3939bd",
	"8000000b17217ff81bef9c551590cf83",
	"800000058b90bfdd4e39cd52c0cfa27c",
	"80000002c5c85fe6f72d669e0e76e411",
	"8000000162e42ff18f9ad35186d0df28",
	"80000000b17217f84cce71aa0dcfffe7",
	"8000000058b90bfc07a77ad56ed22aaa",
	"800000002c5c85fdfc23cdead40da8d6",
	"80000000162e42fefc25eb1571853a66",
	"800000000b17217f7d97f692baacded5",
	"80000000058b90bfbead3b8b5dd254d7",
	"8000000002c5c85fdf4eedd62f084e67",
	"800000000162e42fefa58aef378bf586",
	"8000000000b17217f7d24a78a3c7ef02",
	"800000000058b90bfbe9067c93e474a6",
	"80000000002c5c85fdf47b8e5a72599f",
	"8000000000162e42fefa3bdb315934a2",
	"80000000000b17217f7d1d7299b49c46",
	"8000000000058b90bfbe8e9a8d1c4ea0",
	"800000000002c5c85fdf4745969ea76f",
	"80000000000162e42fefa3a0df5373bf",
	"800000000000b17217f7d1cff4aac1e1",
	"80000000000058b90bfbe8e7db95a2f1",
	"8000000000002c5c85fdf473e61ae1f8",
	"800000000000162e42fefa39f121751c",
	"8000000000000b17217f7d1cf815bb96",
	"800000000000058b90bfbe8e7bec1e0d",
	"80000000000002c5c85fdf473dee5f17",
	"8000000000000162e42fefa39ef5438f",
	"80000000000000b17217f7d1cf7a26c8",
	"8000000000000058b90bfbe8e7bcf4a4",
	"800000000000002c5c85fdf473de72a2",
	"80000000000000162e42fefa39ef3765",
	"800000000000000b17217f7d1cf79b37",
	"80000000000000058b90bfbe8e7bcd7d",
	"8000000000000002c5c85fdf473de6b6",
	"800000000000000162e42fefa39ef359",
	"8000000000000000b17217f7d1cf79ac",
	"800000000000000058b90bfbe8e7bcd6",
}

var kQ127 = func() (k [64]U) {
	for i, s := range kHex {
		k[i] = hx("0x" + s)
	}
	return
}()

var one127 = lsh(n(1), 127)

// mulQ127 is (a · b) >> 127 for two Q1.127 values in [1, 2).
func mulQ127(a, b U) U { return rsh(mul(a, b), 127) }

// log2X64 is fixed::log2_x64: log2(n) for n ≥ 1 as Q64.64, rounded down; false for n = 0.
func log2X64(v U) (U, bool) {
	if isZ(v) {
		return zero, false
	}
	msb := uint(v.BitLen() - 1)
	m := lsh(v, 127-msb)
	var frac uint64
	for i := 0; i < 64; i++ {
		p := mul(m, m)
		bit := u64(rsh(p, 255))
		frac = frac<<1 | bit
		if bit == 1 {
			m = rsh(p, 128)
		} else {
			m = rsh(p, 127)
		}
	}
	return or(lsh(n(uint64(msb)), 64), n(frac)), true
}

// exp2X64 is fixed::exp2_x64: 2^y for y in Q64.64, rounded down; false when y ≥ 64.
func exp2X64(y U) (U, bool) {
	ip := rsh(y, 64)
	if !ip.IsUint64() || ip.Uint64() >= 64 {
		return zero, false
	}
	r := one127
	f := y.Uint64() // the low 64 bits: the fraction
	for i := range kQ127 {
		if f == 0 {
			break
		}
		if f>>63 == 1 {
			r = mulQ127(r, kQ127[i])
		}
		f <<= 1
	}
	return rsh(r, uint(63-ip.Uint64())), true
}
