package protocol

import (
	"crypto/sha256"
	"embed"
	"fmt"
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
	// Only exact embedded package bytes can pin a graph, at most one per ID.
	// The complete admission key must still match when reading a pinned graph.
	presets map[string]presetCompilation
}

var processCompilations = compilationCache{
	entries: make(map[compilationKey]weak.Pointer[Compiled]),
	flights: make(map[compilationKey]*compilationFlight),
	presets: make(map[string]presetCompilation),
}

func (compiler *Compiler) compilationKey(raw []byte) compilationKey {
	return compilationKey{version: CompilerVersion, limits: compiler.limits, features: compiler.featureHash, source: sha256.Sum256(raw)}
}

func (cache *compilationCache) compile(key compilationKey, build func() (*Compiled, []ConversionIssue)) (*Compiled, []ConversionIssue) {
	cache.mu.Lock()
	for id, preset := range cache.presets {
		if preset.key == key && isEmbeddedPreset(id, key, preset.compiled) {
			cache.mu.Unlock()
			return preset.compiled, nil
		}
	}
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

type presetCompilation struct {
	key      compilationKey
	compiled *Compiled
}

// Embed here as well as in builtin so the cache can authenticate source bytes
// without importing builtin (which depends on protocol) or trusting a caller.
//
//go:embed builtin/packages/*/spec.json builtin/packages/*/mapping.json
var embeddedPresetSources embed.FS

type embeddedPresetSource struct {
	value Value
	hash  [32]byte
}

var embeddedPresets = sync.OnceValue(func() map[string]embeddedPresetSource {
	presets := make(map[string]embeddedPresetSource)
	entries, _ := embeddedPresetSources.ReadDir("builtin/packages")
	for _, entry := range entries {
		value, err := embeddedPresetPackage(entry.Name())
		if err != nil {
			continue // Fail closed: an unreadable package can never be pinned.
		}
		identity, err := readValuePointer(value, []string{"spec", "id"})
		var id string
		if err == nil && identity.Decode(&id) == nil && IsPresetProtocolID(id) {
			presets[id] = embeddedPresetSource{value: value, hash: sha256.Sum256(value.Bytes())}
		}
	}
	return presets
})

func embeddedPresetPackage(name string) (Value, error) {
	spec, err := embeddedPresetSources.ReadFile("builtin/packages/" + name + "/spec.json")
	if err != nil {
		return Value{}, err
	}
	mapping, err := embeddedPresetSources.ReadFile("builtin/packages/" + name + "/mapping.json")
	if err != nil {
		return Value{}, err
	}
	// Identical to builtin.Packages and CompileSources; do not normalize JSON.
	return ParseValue([]byte(fmt.Sprintf(`{"schemaVersion":%d,"spec":%s,"mapping":%s}`, ProtocolPackageSchemaVersion, spec, mapping)))
}

func isEmbeddedPreset(id string, key compilationKey, compiled *Compiled) bool {
	expected, exists := embeddedPresets()[id]
	return exists && compiled != nil && compiled.identity.DefinitionID == id && key.source == expected.hash
}

func (cache *compilationCache) admitPreset(id string, key compilationKey, compiled *Compiled) {
	if !isEmbeddedPreset(id, key, compiled) {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.presets[id].compiled == nil {
		cache.presets[id] = presetCompilation{key: key, compiled: compiled}
	}
}
