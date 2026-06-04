package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func New(prefix string) string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		if prefix == "" {
			return fmt.Sprintf("%d", time.Now().UnixNano())
		}
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}

	id := hex.EncodeToString(bytes[:])
	if prefix == "" {
		return id
	}
	return prefix + "_" + id
}
