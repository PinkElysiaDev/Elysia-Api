package protocol

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// White-box resource tests intentionally change limits and replace map entries;
// these are not legal production mutations. Detach exactly their mutation
// targets, including the Agent output slice modified by compilePackageAgent.
// Runtime plans/validators remain immutable and shared, as in production.
func detachedCompiledFixture(compiled *Compiled) *Compiled {
	fixture := *compiled
	fixture.verification = &verificationMemo{}
	fixture.mappings = maps.Clone(compiled.mappings)
	fixture.wireContracts = maps.Clone(compiled.wireContracts)
	fixture.operations = maps.Clone(compiled.operations)
	fixture.agentClientOutput = slices.Clone(compiled.agentClientOutput)
	return &fixture
}

// Guard the shared preset graphs against future white-box tests mutating a
// deeper pointer that the fixture detach intentionally does not clone.
func TestMain(m *testing.M) {
	code := m.Run()
	processCompilations.mu.Lock()
	presets := maps.Clone(processCompilations.presets)
	keys := make(map[*Compiled]compilationKey, len(presets))
	for key, pointer := range processCompilations.entries {
		if compiled := pointer.Value(); compiled != nil {
			keys[compiled] = key
		}
	}
	processCompilations.mu.Unlock()
	for id, compiled := range presets {
		key := keys[compiled]
		compiler, err := NewCompiler(key.limits, []string{featureWebSocketTransport, featureAsyncTasks})
		var document ProtocolPackage
		if err == nil {
			err = decodeContract(compiled.definition.Bytes(), &document)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "preset cache sentinel:", id, err)
			code = 1
			continue
		}
		reference, issues := compiler.compilePackage(document) // deliberately uncached
		if IssuesError(issues) != nil || compiledGraphFingerprint(compiled) != compiledGraphFingerprint(reference) {
			fmt.Fprintln(os.Stderr, "preset cache sentinel: shared graph mutated:", id, issues)
			code = 1
		}
	}
	os.Exit(code)
}

func compiledGraphFingerprint(compiled *Compiled) [32]byte {
	digest := sha256.New()
	fingerprintValue(digest, reflect.ValueOf(compiled), make(map[uintptr]bool))
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

// Traverse pointer contents, not addresses, and detect cycles on the current
// stack only so harmless DAG sharing does not affect the fingerprint. Function
// code addresses distinguish replacements without inspecting closure internals.
func fingerprintValue(digest hash.Hash, value reflect.Value, stack map[uintptr]bool) {
	if !value.IsValid() {
		fmt.Fprint(digest, "nil;")
		return
	}
	fmt.Fprint(digest, value.Type(), ":")
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			fmt.Fprint(digest, "nil;")
			return
		}
		pointer := value.Pointer()
		if stack[pointer] {
			fmt.Fprint(digest, "cycle;")
			return
		}
		stack[pointer] = true
		fingerprintValue(digest, value.Elem(), stack)
		delete(stack, pointer)
	case reflect.Interface:
		fingerprintValue(digest, value.Elem(), stack)
	case reflect.Struct:
		for index := range value.NumField() {
			if value.Type() == reflect.TypeFor[Compiled]() && value.Type().Field(index).Name == "verification" {
				continue // separately synchronized, intentionally mutable evidence
			}
			fmt.Fprint(digest, value.Type().Field(index).Name)
			fingerprintValue(digest, value.Field(index), stack)
		}
	case reflect.Map:
		keys := value.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, key := range keys {
			fingerprintValue(digest, key, stack)
			fingerprintValue(digest, value.MapIndex(key), stack)
		}
	case reflect.Array, reflect.Slice:
		fmt.Fprint(digest, value.Len(), ";")
		for index := range value.Len() {
			fingerprintValue(digest, value.Index(index), stack)
		}
	case reflect.String:
		fmt.Fprintf(digest, "%q;", value.String())
	case reflect.Bool:
		fmt.Fprint(digest, value.Bool(), ";")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fmt.Fprint(digest, value.Int(), ";")
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		fmt.Fprint(digest, value.Uint(), ";")
	case reflect.Float32, reflect.Float64:
		fmt.Fprint(digest, value.Float(), ";")
	case reflect.Func, reflect.UnsafePointer, reflect.Chan:
		fmt.Fprint(digest, value.Pointer(), ";")
	}
}
