package main

import (
	"log"
	"sync"
	"sync/atomic"
)

// Shared data files (emoji trie, spell-check dictionary) are loaded lazily on
// the first FocusIn that needs them. Several engines (VI, EN) can get FocusIn
// at the same time, so loading is serialised and done at most once; a missing
// file is logged instead of crashing the engine.

var dataLoadMu sync.Mutex
var emojiLoaded, dictLoaded bool

// dictionaryOK is set once the spell-check dictionary is usable. Without it
// every Vietnamese word would count as misspelled and be reverted.
var dictionaryOK atomic.Bool

func ensureEmojiTrie() {
	dataLoadMu.Lock()
	defer dataLoadMu.Unlock()
	if emojiLoaded {
		return
	}
	emojiLoaded = true
	trie, err := loadEmojiOne(DictEmojiOne)
	if err != nil {
		log.Printf("emoji: load %s: %v", DictEmojiOne, err)
		return
	}
	emojiTrie = trie
}

func ensureDictionary() {
	dataLoadMu.Lock()
	defer dataLoadMu.Unlock()
	if dictLoaded {
		return
	}
	dictLoaded = true
	d, err := loadDictionary(DictVietnameseCm)
	if err != nil {
		log.Printf("spell check: load %s: %v", DictVietnameseCm, err)
		return
	}
	if len(d) == 0 {
		log.Printf("spell check: %s is empty: using the spelling rules", DictVietnameseCm)
		return
	}
	dictionary = d
	dictionaryOK.Store(true)
}
