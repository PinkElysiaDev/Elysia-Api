package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestChildStorageGrowthPreservesViewsAndOrder(t *testing.T) {
	for _, count := range []int{0, 1, 2, 3, objectReaderChildren, objectReaderChildren + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var entries []string
			for index := range count {
				entries = append(entries, fmt.Sprintf(`"f%d":{"n":900719925474099%d}`, index, index))
			}
			input := parseTestValue(t, `{`+strings.Join(entries, ",")+`}`)
			reader := newPlanObjectReader(input, objectReuseReads)
			var selected []Value
			var initialViews [2]*objectFieldView
			for index := range count {
				key := fmt.Sprintf("f%d", index)
				child, err := reader.readOwnedMember(input, key)
				if err != nil {
					t.Fatal(err)
				}
				selected = append(selected, child)
				want, wantErr := readValuePointer(input, []string{key, "n"})
				got, gotErr := reader.readOwnedMember(child, "n")
				if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatal("growth changed selected value or error", index, got, want, gotErr, wantErr)
				}
				if index < len(initialViews) {
					initialViews[index] = reader.children[index].fields
				}
				capacity := objectReaderInitialChildren
				if index >= objectReaderInitialChildren {
					capacity = objectReaderChildren
				}
				if cap(reader.children) != capacity {
					t.Fatal("child storage did not use the initial capacity then one growth", index, cap(reader.children), capacity)
				}
			}
			if len(reader.children) != min(count, objectReaderChildren) {
				t.Fatal("logical descendant bound changed")
			}
			for index, cached := range reader.children {
				if cached.identity != spanIdentity(selected[index].raw) || reader.fieldsFor(selected[index]) != cached.fields {
					t.Fatal("growth changed cache order or field-table identity", index)
				}
				if index < len(initialViews) && cached.fields != initialViews[index] {
					t.Fatal("growth reconstructed an existing immutable view", index)
				}
			}
			for index, child := range selected {
				want, wantErr := readValuePointer(input, []string{fmt.Sprintf("f%d", index), "n"})
				got, gotErr := reader.readOwnedMember(child, "n")
				if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatal("post-growth hit changed output", index)
				}
			}
			if count > objectReaderChildren && reader.fieldsFor(selected[objectReaderChildren]) != nil {
				t.Fatal("ninth child became retained")
			}
			if count == 0 && reader.children != nil {
				t.Fatal("empty reader allocated child storage")
			}
			reader.releaseItem(nil)
			if !reflect.DeepEqual(*reader, planObjectReader{}) {
				t.Fatal("release retained child backing storage")
			}
		})
	}
}

func TestChildStorageGrowthKeepsParentOwnershipAndReset(t *testing.T) {
	input := parseTestValue(t, `{"first":{"n":"one"},"second":{"n":"two"},"third":{"n":"small"},"padding":"`+strings.Repeat("x", requestViewMinBytes*2)+`"}`)
	parent := newPlanObjectReader(input, objectReuseReads)
	var selected []Value
	for _, key := range []string{"first", "second", "third"} {
		child, err := parent.readOwnedMember(input, key)
		if err != nil {
			t.Fatal(err)
		}
		selected = append(selected, child)
		if _, err := parent.readOwnedMember(child, "n"); err != nil {
			t.Fatal(err)
		}
	}
	view := parent.fieldsFor(selected[2])
	reader := &planObjectReader{root: spanIdentity(selected[2].raw), fields: view, parent: parent}
	if reader.fieldsFor(selected[0]) != parent.children[0].fields || reader.ownerFor(input) != parent {
		t.Fatal("nested lookup lost enclosing owner after growth")
	}
	owned, err := readValuePointerWithObjectReader(input, []string{"third", "n"}, requestValueReader(input), reader)
	if err != nil || owned.raw != `"small"` || sharesValueStorage(input, owned) {
		t.Fatal("small output retained the large frame", err)
	}
	invalid := Value{raw: `"scalar"`}
	_, wantErr := invalid.readObjectView(nil)
	_, gotErr := reader.readOwnedMember(invalid, "n")
	if gotErr == nil || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || len(reader.children) != 0 {
		t.Fatal("parse failure changed or allocated retained child storage", gotErr, wantErr)
	}
	reader.releaseItem(gotErr)
	if !reflect.DeepEqual(*reader, planObjectReader{}) || parent.fieldsFor(selected[2]) != view {
		t.Fatal("failed nested reader release changed parent ownership")
	}
	next := parseTestValue(t, `{"child":{"n":"next"}}`)
	*reader = planObjectReader{root: spanIdentity(next.raw)}
	got, err := readValuePointerWithObjectReader(next, []string{"child", "n"}, nil, reader)
	if err != nil || got.raw != `"next"` || len(reader.children) != 1 || cap(reader.children) != objectReaderInitialChildren {
		t.Fatal("reused reader did not restart child storage", got, err)
	}
	reader.releaseItem(nil)
	parent.releaseItem(nil)
	if owned.raw != `"small"` || !reflect.DeepEqual(*reader, planObjectReader{}) || !reflect.DeepEqual(*parent, planObjectReader{}) {
		t.Fatal("release changed the owned result or retained a scope")
	}
}
