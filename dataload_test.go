package main

import (
	"sync"
	"testing"
)

// Concurrent FocusIn of several engines loads shared data once, without a
// race and without panicking when a file is missing (run with -race).
func TestEnsureDataConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ensureEmojiTrie()
			ensureDictionary()
		}()
	}
	wg.Wait()
	if emojiTrie == nil {
		t.Fatalf("emoji trie became nil")
	}
}
