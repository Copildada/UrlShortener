package encoding

import (
	"errors"
	"strings"
)

// Base62 provides base62 encoding/decoding for collision-free short codes
type Base62 struct {
	alphabet string
}

// NewBase62 creates a new Base62 encoder
func NewBase62() *Base62 {
	// alphabet: 0-9 (10) + a-z (26) + A-Z (26) = 62 characters
	return &Base62{
		alphabet: "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
	}
}

// Encode converts an integer to a base62 string
func (b *Base62) Encode(num int64) string {
	if num == 0 {
		return "0"
	}

	var result strings.Builder
	base := int64(62)

	for num > 0 {
		remainder := num % base
		result.WriteByte(b.alphabet[remainder])
		num = num / base
	}

	// Reverse the result since we built it backwards
	encoded := result.String()
	runes := []rune(encoded)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}

	return string(runes)
}

// Decode converts a base62 string back to an integer
func (b *Base62) Decode(encoded string) (int64, error) {
	if encoded == "" {
		return 0, errors.New("empty string")
	}

	var result int64
	base := int64(62)

	for _, char := range encoded {
		index := strings.IndexRune(b.alphabet, char)
		if index == -1 {
			return 0, errors.New("invalid character in base62 string")
		}

		result = result*base + int64(index)
	}

	return result, nil
}
