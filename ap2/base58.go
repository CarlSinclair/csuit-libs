package ap2

import (
	"fmt"
	"math/big"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index = func() map[byte]int {
	m := map[byte]int{}
	for i := 0; i < len(base58Alphabet); i++ {
		m[base58Alphabet[i]] = i
	}
	return m
}()

// base58Decode decodes a base58btc string (as used by multibase "z" for
// did:key). Leading '1' characters map to leading zero bytes.
func base58Decode(s string) ([]byte, error) {
	result := big.NewInt(0)
	radix := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		v, ok := base58Index[s[i]]
		if !ok {
			return nil, fmt.Errorf("base58: invalid character %q", s[i])
		}
		result.Mul(result, radix)
		result.Add(result, big.NewInt(int64(v)))
	}
	decoded := result.Bytes()
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	return append(make([]byte, zeros), decoded...), nil
}
