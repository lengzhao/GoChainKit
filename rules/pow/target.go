package pow

import (
	"fmt"
	"math/big"
)

func DecodeTarget(bits uint32) (*big.Int, error) {
	size := bits >> 24
	word := bits & 0x007fffff
	if bits&0x00800000 != 0 || word == 0 || size > 34 {
		return nil, fmt.Errorf("invalid compact target")
	}
	v := new(big.Int).SetUint64(uint64(word))
	if size <= 3 {
		v.Rsh(v, uint(8*(3-size)))
	} else {
		v.Lsh(v, uint(8*(size-3)))
	}
	if v.Sign() <= 0 || v.BitLen() > 256 || EncodeTarget(v) != bits {
		return nil, fmt.Errorf("noncanonical compact target")
	}
	return v, nil
}
func EncodeTarget(v *big.Int) uint32 {
	if v == nil || v.Sign() <= 0 {
		return 0
	}
	size := uint32((v.BitLen() + 7) / 8)
	tmp := new(big.Int).Set(v)
	if size <= 3 {
		tmp.Lsh(tmp, uint(8*(3-size)))
	} else {
		tmp.Rsh(tmp, uint(8*(size-3)))
	}
	word := uint32(tmp.Uint64())
	if word&0x00800000 != 0 {
		word >>= 8
		size++
	}
	return size<<24 | word
}
func WorkFor(target *big.Int) *big.Int {
	if target == nil || target.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Div(new(big.Int).Lsh(big.NewInt(1), 256), new(big.Int).Add(target, big.NewInt(1)))
}
