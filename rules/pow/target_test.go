package pow

import (
	"math/big"
	"testing"
)

func TestCanonicalCompactTarget(t *testing.T) {
	for _, bits := range []uint32{0, 0x01800001, 0x01010001, 0x23000001, 0x02000100} {
		if _, e := DecodeTarget(bits); e == nil {
			t.Fatalf("accepted noncanonical %x", bits)
		}
	}
	for _, bits := range []uint32{0x01010000, 0x1d00ffff, 0x207fffff} {
		v, e := DecodeTarget(bits)
		if e != nil {
			t.Fatal(e)
		}
		if EncodeTarget(v) != bits {
			t.Fatalf("roundtrip %x", bits)
		}
	}
	v, _ := DecodeTarget(0x207fffff)
	if WorkFor(v).Cmp(new(big.Int).Div(new(big.Int).Lsh(big.NewInt(1), 256), new(big.Int).Add(v, big.NewInt(1)))) != 0 {
		t.Fatal("wrong work")
	}
}
