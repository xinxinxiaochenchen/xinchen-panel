package id

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

func NewV7() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate UUID randomness: %w", err)
	}
	millis := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint64(bytes[:8], millis<<16|uint64(bytes[6])<<8|uint64(bytes[7]))
	bytes[6] = bytes[6]&0x0f | 0x70
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
