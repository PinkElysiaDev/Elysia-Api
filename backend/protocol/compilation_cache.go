package protocol

import (
	"crypto/sha256"
	"runtime"
	"sync"
	"weak"
)

// Source spelling is part of admission: normalization must not erase duplicate
// keys, unknown fields, explicit zeroes, or authored resource usage.
type compilationKey struct {
	version  string
	limits   Limits
	features [32]byte
	source   [32]byte
}

type compilationFlight struct {
	done     chan struct{}
	compiled *Compiled
	issues   []ConversionIssue
}

type compilationCache struct {
	mu      sync.Mutex
	entries map[compilationKey]weak.Pointer[Compiled]
	flights map[compilationKey]*compilationFlight
	// Only the first explicitly admitted source for each shipped preset is
	// pinned. Production registries already keep these same graphs live. Custom
	// definitions and alternative spellings/configurations remain weakly held.
	presets map[string]*Compiled
}

var processCompilations = compilationCache{
	entries: make(map[compilationKey]weak.Pointer[Compiled]),
	flights: make(map[compilationKey]*compilationFlight),
	presets: make(map[string]*Compiled),
}

func (compiler *Compiler) compilationKey(raw []byte) compilationKey {
	return compilationKey{version: CompilerVersion, limits: compiler.limits, features: compiler.featureHash, source: sha256.Sum256(raw)}
}

func (cache *compilationCache) compile(key compilationKey, build func() (*Compiled, []ConversionIssue)) (*Compiled, []ConversionIssue) {
	cache.mu.Lock()
	if compiled := cache.entries[key].Value(); compiled != nil {
		cache.mu.Unlock()
		return compiled, nil
	}
	if flight := cache.flights[key]; flight != nil {
		cache.mu.Unlock()
		<-flight.done
		if flight.compiled == nil && len(flight.issues) == 0 {
			return cache.compile(key, build)
		}
		return flight.compiled, append([]ConversionIssue(nil), flight.issues...)
	}
	flight := &compilationFlight{done: make(chan struct{})}
	cache.flights[key] = flight
	cache.mu.Unlock()
	// A panic must not strand waiters or permanently poison the key. The leader
	// still panics; followers retry instead of receiving a false success.
	defer func() {
		cache.mu.Lock()
		delete(cache.flights, key)
		close(flight.done)
		cache.mu.Unlock()
	}()
	compiled, issues := build()
	cache.mu.Lock()
	flight.compiled, flight.issues = compiled, append([]ConversionIssue(nil), issues...)
	if compiled != nil && len(issues) == 0 {
		pointer := weak.Make(compiled)
		cache.entries[key] = pointer
		// Neither this argument nor the cleanup closure retains compiled. The
		// identity check protects a replacement from a late old cleanup.
		runtime.AddCleanup(compiled, func(entry compilationCleanup) {
			entry.cache.mu.Lock()
			defer entry.cache.mu.Unlock()
			if entry.cache.entries[entry.key] == entry.pointer {
				delete(entry.cache.entries, entry.key)
			}
		}, compilationCleanup{cache, key, pointer})
	}
	cache.mu.Unlock()
	return compiled, issues
}

type compilationCleanup struct {
	cache   *compilationCache
	key     compilationKey
	pointer weak.Pointer[Compiled]
}

func (cache *compilationCache) admitPreset(id string, compiled *Compiled) {
	if !IsPresetProtocolID(id) || compiled.identity.DefinitionID != id {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.presets[id] == nil {
		cache.presets[id] = compiled
	}
}
