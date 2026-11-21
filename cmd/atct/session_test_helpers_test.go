package main

import (
	"sync"
)

var cliTestSessionIDs = struct {
	sync.Mutex
	ids  map[string]int64
	next int64
}{ids: make(map[string]int64), next: 100000}

func cliTestSessionID(label string) int64 {
	cliTestSessionIDs.Lock()
	defer cliTestSessionIDs.Unlock()
	if id, ok := cliTestSessionIDs.ids[label]; ok {
		return id
	}
	cliTestSessionIDs.next++
	cliTestSessionIDs.ids[label] = cliTestSessionIDs.next
	return cliTestSessionIDs.next
}
