package storage

import (
	"fmt"
	"hash/fnv"
)

func URLHash(link string) string {
	h := fnv.New32()
	_, _ = h.Write([]byte(link))
	return fmt.Sprint(h.Sum32())
}
