package server

import (
	"fmt"
	"sync"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

type protocolRevisionUses struct {
	mu     sync.Mutex
	counts map[string]int
}

func (uses *protocolRevisionUses) acquire(id, hash string) func() {
	uses.mu.Lock()
	if uses.counts == nil {
		uses.counts = map[string]int{}
	}
	key := id + "/" + hash
	uses.counts[key]++
	uses.mu.Unlock()
	return func() {
		uses.mu.Lock()
		defer uses.mu.Unlock()
		uses.counts[key]--
		if uses.counts[key] == 0 {
			delete(uses.counts, key)
		}
	}
}
func (uses *protocolRevisionUses) references(id, hash string) []storage.ProtocolReference {
	uses.mu.Lock()
	defer uses.mu.Unlock()
	items := []storage.ProtocolReference{}
	if count := uses.counts[id+"/"+hash]; count > 0 {
		items = append(items, storage.ProtocolReference{Kind: "in_flight", ID: fmt.Sprint(count), Name: "进行中的请求或会话"})
	}
	return items
}
func (uses *protocolRevisionUses) exclusive(id, hash string, commit func() error) error {
	uses.mu.Lock()
	defer uses.mu.Unlock()
	if uses.counts[id+"/"+hash] > 0 {
		return protocol.ErrRevisionConflict
	}
	return commit()
}
